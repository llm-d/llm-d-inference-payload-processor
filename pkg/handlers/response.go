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
	"bytes"
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
	}
	// Always respond to response headers so Envoy proceeds with body chunks.
	// In STREAMED/FULL_DUPLEX_STREAMED mode, Envoy blocks until we respond.
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
	logger := log.FromContext(ctx)
	if len(s.responsePlugins) == 0 {
		return s.generateEmptyResponseBodyResponse(responseBodyBytes), nil
	}

	if err := json.Unmarshal(responseBodyBytes, &reqCtx.Response.Body); err != nil {
		// Try parsing as SSE (Server-Sent Events) — streaming responses from providers
		// like Anthropic use SSE format which isn't valid JSON.
		if sseBody, sseErr := parseSSEResponseBody(responseBodyBytes); sseErr == nil && sseBody != nil {
			reqCtx.Response.Body = sseBody
			logger.V(logutil.VERBOSE).Info("parsed SSE response body for response plugins")
		} else {
			logger.Error(err, "Failed to parse response body as JSON or SSE, skipping response plugins")
			return s.generateEmptyResponseBodyResponse(responseBodyBytes), nil
		}
	}

	if err := s.runResponsePlugins(ctx, reqCtx.CycleState, reqCtx.Response); err != nil {
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

// parseSSEResponseBody extracts a composite response body from an SSE (Server-Sent Events)
// stream. It scans all "data:" lines for JSON objects and merges usage/model fields into
// a single map that response plugins can process. This enables usage-tracking and metering
// plugins to work with streaming responses from providers like Anthropic and OpenAI.
func parseSSEResponseBody(body []byte) (map[string]any, error) {
	result := map[string]any{}
	lines := bytes.Split(body, []byte("\n"))

	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(line[5:])
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}

		var event map[string]any
		if err := json.Unmarshal(data, &event); err != nil {
			continue
		}

		if model, ok := event["model"].(string); ok && model != "" {
			result["model"] = model
		}

		// Check for usage at top level (Anthropic) or nested in response (OpenAI Responses API)
		usage, _ := event["usage"].(map[string]any)
		if usage == nil {
			if resp, ok := event["response"].(map[string]any); ok {
				usage, _ = resp["usage"].(map[string]any)
				if m, ok := resp["model"].(string); ok && m != "" {
					result["model"] = m
				}
			}
		}
		if usage != nil {
			existing, _ := result["usage"].(map[string]any)
			if existing == nil {
				existing = map[string]any{}
			}
			for k, v := range usage {
				existing[k] = v
			}
			result["usage"] = existing
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no parseable SSE data events found")
	}

	return result, nil
}

// runResponsePlugins executes response plugins in the order they were registered.
func (s *Server) runResponsePlugins(ctx context.Context, cycleState *plugin.CycleState, response *requesthandling.InferenceResponse) error {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)

	// Cache verbose logger and check Enabled() once to avoid per-iteration
	// allocations from argument boxing when logging at that level is disabled.
	verboseLogger := logger.V(logutil.VERBOSE)
	verboseEnabled := verboseLogger.Enabled()

	var err error
	for _, plugin := range s.responsePlugins {
		if verboseEnabled {
			verboseLogger.Info("Executing response plugin", "plugin", plugin.TypedName())
		}
		before := time.Now()
		err = plugin.ProcessResponse(ctx, cycleState, response)
		metrics.RecordPluginProcessingLatency(responsePluginExtensionPoint, plugin.TypedName().Type, plugin.TypedName().Name, time.Since(before))
		if err != nil {
			logger.Error(err, "Failed to execute response plugin", "plugin", plugin.TypedName())
			return err
		}
	}

	return nil
}
