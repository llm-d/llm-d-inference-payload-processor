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
	datasource "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/metrics"
)

// HandleResponseHeaders extracts response headers into reqCtx and returns
// the ext-proc header response.
func (s *Server) HandleResponseHeaders(ctx context.Context, reqCtx *RequestContext, headers *eppb.HttpHeaders) []*eppb.ProcessingResponse {
	if headers != nil && headers.Headers != nil {
		for _, header := range headers.Headers.Headers {
			reqCtx.Response.Headers[header.Key] = envoy.GetHeaderValue(header)
		}
	}

	if !headers.GetEndOfStream() {
		log.FromContext(ctx).V(logutil.VERBOSE).Info("captured response headers, deferring response until body arrives...")
		return nil
	}
	// EndOfStream means no body is expected, return HeadersResponse immediately
	return []*eppb.ProcessingResponse{
		{
			Response: &eppb.ProcessingResponse_ResponseHeaders{
				ResponseHeaders: &eppb.HeadersResponse{},
			},
		},
	}
}

// HandleResponseBody handles response bodies by executing response plugins in order.
func (s *Server) HandleResponseBody(ctx context.Context, reqCtx *RequestContext, responseBodyBytes []byte) ([]*eppb.ProcessingResponse, error) {
	// Notify the data layer of the completed response.
	s.eventNotifier.Notify(datasource.Event{
		Type: datasource.ResponseEventType,
		Payload: datasource.ResponsePayload{
			Request:  reqCtx.Request,
			Response: reqCtx.Response,
			Duration: reqCtx.ResponseCompleteTimestamp.Sub(reqCtx.RequestReceivedTimestamp),
			TTFT:     reqCtx.ResponseFirstChunkTimestamp.Sub(reqCtx.RequestSentTimestamp),
		},
	})

	logger := log.FromContext(ctx)
	// if a bodiless request is sent, profile could be left nil. check to avoid panic with nil exception
	if reqCtx.Profile == nil || len(reqCtx.Profile.ResponsePlugins) == 0 {
		return s.generateEmptyResponseBodyResponse(responseBodyBytes), nil
	}

	if err := json.Unmarshal(responseBodyBytes, &reqCtx.Response.Body); err != nil {
		logger.Error(err, "Failed to parse response body as JSON, skipping response plugins")
		return s.generateEmptyResponseBodyResponse(responseBodyBytes), nil
	}

	if err := s.runResponsePlugins(ctx, reqCtx.CycleState, reqCtx.Response, reqCtx.Profile.ResponsePlugins); err != nil {
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

// generateEmptyResponseBodyResponse builds a streaming response with an empty
// ResponseHeaders followed by chunked body responses via AddStreamedResponseBody.
func (s *Server) generateEmptyResponseBodyResponse(responseBodyBytes []byte) []*eppb.ProcessingResponse {
	responses := []*eppb.ProcessingResponse{
		{
			Response: &eppb.ProcessingResponse_ResponseHeaders{
				ResponseHeaders: &eppb.HeadersResponse{},
			},
		},
	}
	responses = envoy.AddStreamedResponseBody(responses, responseBodyBytes)
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
