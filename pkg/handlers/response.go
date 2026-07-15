/*
Copyright 2026 The llm-d Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	eppb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"sigs.k8s.io/controller-runtime/pkg/log"

	envoy "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/envoy"
	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/metrics"
)

// HandleResponseHeaders extracts response headers into reqCtx, runs any
// response-headers post-processors, and returns the ext-proc header response.
func (s *Server) HandleResponseHeaders(ctx context.Context, reqCtx *RequestContext, headers *eppb.HttpHeaders) ([]*eppb.ProcessingResponse, error) {
	if headers != nil && headers.Headers != nil {
		for _, header := range headers.Headers.Headers {
			reqCtx.Response.Headers[header.Key] = envoy.GetHeaderValue(header)
		}
	}

	if err := s.runResponseHeadersProcessors(ctx, reqCtx.CycleState, reqCtx.Response); err != nil {
		return nil, err
	}

	if !headers.GetEndOfStream() {
		log.FromContext(ctx).V(logutil.VERBOSE).Info("captured response headers, deferring response until body arrives...")
		return nil, nil
	}
	// EndOfStream means no body is expected, return HeadersResponse immediately
	return []*eppb.ProcessingResponse{
		{
			Response: &eppb.ProcessingResponse_ResponseHeaders{
				ResponseHeaders: buildHeadersResponse(reqCtx),
			},
		},
	}, nil
}

// HandleResponseBody handles response bodies by executing response plugins in order.
func (s *Server) HandleResponseBody(ctx context.Context, reqCtx *RequestContext, responseBodyBytes []byte) ([]*eppb.ProcessingResponse, error) {
	logger := log.FromContext(ctx)

	hasProfilePlugins := len(reqCtx.Profile.ResponsePlugins) > 0
	hasPostProcessors := len(s.postProcessors) > 0

	if !hasProfilePlugins && !hasPostProcessors {
		return s.generatePassthroughResponseBodyResponse(reqCtx, responseBodyBytes), nil
	}

	if err := json.Unmarshal(responseBodyBytes, &reqCtx.Response.Body); err != nil {
		logger.Error(err, "Failed to parse response body as JSON, skipping response plugins")
		return s.generatePassthroughResponseBodyResponse(reqCtx, responseBodyBytes), nil
	}

	if hasProfilePlugins {
		if err := s.runResponsePlugins(ctx, reqCtx.CycleState, reqCtx.Response, reqCtx.Profile.ResponsePlugins); err != nil {
			return nil, err
		}
	}

	if err := s.runResponsePlugins(ctx, reqCtx.CycleState, reqCtx.Response, s.postProcessors); err != nil {
		return nil, err
	}

	bodyMutated := reqCtx.Response.BodyMutated()
	var mutatedBytes []byte
	if bodyMutated {
		var err error
		mutatedBytes, err = json.Marshal(reqCtx.Response.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal mutated response body - %w", err)
		}
		reqCtx.Response.SetHeader(contentLengthHeader, strconv.Itoa(len(mutatedBytes)))
	}

	var ret []*eppb.ProcessingResponse
	ret = append(ret, &eppb.ProcessingResponse{
		Response: &eppb.ProcessingResponse_ResponseHeaders{
			ResponseHeaders: &eppb.HeadersResponse{
				Response: &eppb.CommonResponse{
					ClearRouteCache: true,
					HeaderMutation: &eppb.HeaderMutation{
						SetHeaders:    envoy.GenerateHeadersMutation(reqCtx.Response.MutatedHeaders()),
						RemoveHeaders: reqCtx.Response.RemovedHeaders(),
					},
				},
			},
		},
	})
	if bodyMutated {
		ret = envoy.AddStreamedResponseBody(ret, mutatedBytes)
	} else {
		ret = envoy.AddStreamedResponseBody(ret, responseBodyBytes)
	}
	return ret, nil
}

// generatePassthroughResponseBodyResponse builds a streaming response with a
// ResponseHeaders (including any header mutations from the response-headers phase)
// followed by chunked body responses via AddStreamedResponseBody.
func (s *Server) generatePassthroughResponseBodyResponse(reqCtx *RequestContext, responseBodyBytes []byte) []*eppb.ProcessingResponse {
	responses := []*eppb.ProcessingResponse{
		{
			Response: &eppb.ProcessingResponse_ResponseHeaders{
				ResponseHeaders: buildHeadersResponse(reqCtx),
			},
		},
	}
	responses = envoy.AddStreamedResponseBody(responses, responseBodyBytes)
	return responses
}

// buildHeadersResponse constructs a HeadersResponse that includes any header
// mutations set during the response-headers phase. Returns an empty
// HeadersResponse when there are no mutations to avoid sending unnecessary
// proto fields.
func buildHeadersResponse(reqCtx *RequestContext) *eppb.HeadersResponse {
	mutatedHeaders := reqCtx.Response.MutatedHeaders()
	removedHeaders := reqCtx.Response.RemovedHeaders()

	if len(mutatedHeaders) == 0 && len(removedHeaders) == 0 {
		return &eppb.HeadersResponse{}
	}

	return &eppb.HeadersResponse{
		Response: &eppb.CommonResponse{
			HeaderMutation: &eppb.HeaderMutation{
				SetHeaders:    envoy.GenerateHeadersMutation(mutatedHeaders),
				RemoveHeaders: removedHeaders,
			},
		},
	}
}

// HandleResponseChunk runs ResponseChunkProcessors on a single response body chunk
// and wraps the result in the ext_proc streaming response format.
func (s *Server) HandleResponseChunk(ctx context.Context, reqCtx *RequestContext, chunkBytes []byte, endOfStream bool) ([]*eppb.ProcessingResponse, error) {
	if len(reqCtx.Profile.ResponseChunkProcessors) == 0 {
		return s.buildStreamedChunkResponse(reqCtx, chunkBytes, endOfStream), nil
	}

	logger := log.FromContext(ctx).V(logutil.DEFAULT)

	chunk := string(chunkBytes)
	reqCtx.Response.ResetChunkState(chunk)

	if err := s.runResponseChunkProcessors(ctx, reqCtx.CycleState, reqCtx.Response, endOfStream, reqCtx.Profile.ResponseChunkProcessors); err != nil {
		logger.Error(err, "Failed to run response chunk processors")
		return nil, err
	}

	outBytes := chunkBytes
	if reqCtx.Response.ChunkMutated() {
		outBytes = []byte(reqCtx.Response.CurrentChunk)
	}

	return s.buildStreamedChunkResponse(reqCtx, outBytes, endOfStream), nil
}

// runResponseChunkProcessors executes chunk processors in the order they were registered.
// Each plugin receives response.CurrentChunk so mutations from earlier plugins are visible
// to later ones in the chain.
func (s *Server) runResponseChunkProcessors(ctx context.Context, cycleState *plugin.CycleState, response *requesthandling.InferenceResponse, isFinal bool, processors []requesthandling.ResponseChunkProcessor) error {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)
	verboseLogger := logger.V(logutil.VERBOSE)

	for _, cp := range processors {
		if verboseLogger.Enabled() {
			verboseLogger.Info("Executing response chunk plugin", "plugin", cp.TypedName())
		}
		before := time.Now()
		err := cp.ProcessResponseChunk(ctx, cycleState, response, isFinal)
		metrics.RecordPluginProcessingLatency(responsePluginExtensionPoint, cp.TypedName().Type, cp.TypedName().Name, time.Since(before))
		if err != nil {
			return err
		}
	}
	return nil
}

// buildStreamedChunkResponse wraps a chunk in the ext_proc streaming response format.
func (s *Server) buildStreamedChunkResponse(reqCtx *RequestContext, chunk []byte, endOfStream bool) []*eppb.ProcessingResponse {
	responses := []*eppb.ProcessingResponse{
		{
			Response: &eppb.ProcessingResponse_ResponseBody{
				ResponseBody: &eppb.BodyResponse{
					Response: &eppb.CommonResponse{
						BodyMutation: &eppb.BodyMutation{
							Mutation: &eppb.BodyMutation_StreamedResponse{
								StreamedResponse: &eppb.StreamedBodyResponse{
									Body:        chunk,
									EndOfStream: endOfStream,
								},
							},
						},
					},
				},
			},
		},
	}

	if !reqCtx.ResponseHeadersSent {
		headerResp := &eppb.ProcessingResponse{
			Response: &eppb.ProcessingResponse_ResponseHeaders{
				ResponseHeaders: buildHeadersResponse(reqCtx),
			},
		}
		responses = append([]*eppb.ProcessingResponse{headerResp}, responses...)
		reqCtx.ResponseHeadersSent = true
	}

	return responses
}

// HandleResponseTrailers handles response trailers.
func (s *Server) HandleResponseTrailers(trailers *eppb.HttpTrailers) ([]*eppb.ProcessingResponse, error) {
	return []*eppb.ProcessingResponse{
		{
			Response: &eppb.ProcessingResponse_ResponseTrailers{
				ResponseTrailers: &eppb.TrailersResponse{},
			},
		},
	}, nil
}

// runResponseHeadersProcessors executes response-headers post-processors in order.
func (s *Server) runResponseHeadersProcessors(ctx context.Context, cycleState *plugin.CycleState, response *requesthandling.InferenceResponse) error {
	if len(s.responseHeadersPostProcessors) == 0 {
		return nil
	}

	logger := log.FromContext(ctx).V(logutil.DEFAULT)
	verboseLogger := logger.V(logutil.VERBOSE)

	for _, hp := range s.responseHeadersPostProcessors {
		if verboseLogger.Enabled() {
			verboseLogger.Info("Executing response headers plugin", "plugin", hp.TypedName())
		}
		before := time.Now()
		if err := hp.ProcessResponseHeaders(ctx, cycleState, response); err != nil {
			logger.Error(err, "Failed to execute response headers plugin", "plugin", hp.TypedName())
			return err
		}
		metrics.RecordPluginProcessingLatency(responsePluginExtensionPoint, hp.TypedName().Type, hp.TypedName().Name, time.Since(before))
	}

	return nil
}

// runResponsePlugins executes response plugins in the order they were registered.
func (s *Server) runResponsePlugins(ctx context.Context, cycleState *plugin.CycleState, response *requesthandling.InferenceResponse, respPlugins []requesthandling.ResponseProcessor) error {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)

	// Cache verbose logger and check Enabled() once to avoid per-iteration
	// allocations from argument boxing when logging at that level is disabled.
	verboseLogger := logger.V(logutil.VERBOSE)
	verboseEnabled := verboseLogger.Enabled()

	var err error
	for _, respPlugin := range respPlugins {
		if verboseEnabled {
			verboseLogger.Info("Executing response plugin", "plugin", respPlugin.TypedName())
		}
		before := time.Now()
		err = respPlugin.ProcessResponse(ctx, cycleState, response)
		metrics.RecordPluginProcessingLatency(responsePluginExtensionPoint, respPlugin.TypedName().Type, respPlugin.TypedName().Name, time.Since(before))
		if err != nil {
			logger.Error(err, "Failed to execute response plugin", "plugin", respPlugin.TypedName())
			return err
		}
	}

	return nil
}
