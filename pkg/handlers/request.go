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
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	eppb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"sigs.k8s.io/controller-runtime/pkg/log"

	envoy "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/envoy"
	errcommon "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/error"
	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	datasource "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/metrics"
)

// HandleRequestHeaders extracts request headers into reqCtx and returns
// the ext-proc header response.
func (s *Server) HandleRequestHeaders(ctx context.Context, reqCtx *RequestContext, headers *eppb.HttpHeaders) []*eppb.ProcessingResponse {
	reqCtx.RequestReceivedTimestamp = time.Now()

	if headers != nil && headers.Headers != nil {
		for _, header := range headers.Headers.Headers {
			reqCtx.Request.Headers[header.Key] = envoy.GetHeaderValue(header)
		}
	}

	if !headers.GetEndOfStream() {
		log.FromContext(ctx).V(logutil.VERBOSE).Info("captured request headers, deferring response until body arrives...")
		return nil
	}
	// EndOfStream means no body is expected, return HeadersResponse immediately
	return []*eppb.ProcessingResponse{
		{
			Response: &eppb.ProcessingResponse_RequestHeaders{
				RequestHeaders: &eppb.HeadersResponse{},
			},
		},
	}
}

// HandleRequestBody parses the raw body bytes into reqCtx.Request.Body and processes the request.
func (s *Server) HandleRequestBody(ctx context.Context, reqCtx *RequestContext, requestBodyBytes []byte) ([]*eppb.ProcessingResponse, error) {
	var ret []*eppb.ProcessingResponse


	// Strip stream_options from raw bytes before parsing — body mutations dont work in FDS mode.
	if bytes.Contains(requestBodyBytes, []byte("stream_options")) {
		log.FromContext(ctx).Info("DEBUG: stream_options found in raw body, stripping", "bodyLen", len(requestBodyBytes), "snippet", string(requestBodyBytes[max(0,bytes.Index(requestBodyBytes, []byte("stream_options"))-10):min(len(requestBodyBytes),bytes.Index(requestBodyBytes, []byte("stream_options"))+60)]))
	}
	requestBodyBytes = stripStreamOptions(requestBodyBytes)
	if bytes.Contains(requestBodyBytes, []byte("stream_options")) {
		log.FromContext(ctx).Error(nil, "DEBUG: stream_options STILL in body after strip!", "bodyLen", len(requestBodyBytes))
	}

	if err := json.Unmarshal(requestBodyBytes, &reqCtx.Request.Body); err != nil {
		return nil, errcommon.Error{Code: errcommon.BadRequest, Msg: fmt.Sprintf("failed to parse request body: %v", err)}
	}

	var err error
	reqCtx.Profile, err = s.profilePicker.Pick(ctx, reqCtx.CycleState, reqCtx.Request, s.profiles)
	if err != nil {
		return nil, errcommon.Error{Code: errcommon.Internal, Msg: fmt.Sprintf("failed to pick a profile: %v", err)}
	}
	if err := s.runRequestPlugins(ctx, reqCtx.CycleState, reqCtx.Request, reqCtx.Profile.RequestPlugins); err != nil {
		return nil, err
	}

	bodyMutated := reqCtx.Request.BodyMutated()
	var mutatedBodyBytes []byte
	if bodyMutated {
		var err error
		mutatedBodyBytes, err = json.Marshal(reqCtx.Request.Body)
		if err != nil {
			return nil, err
		}
		reqCtx.Request.SetHeader(contentLengthHeader, strconv.Itoa(len(mutatedBodyBytes)))
	} else {
		// Always set Content-Length to inform Envoy of the body size that will follow
		reqCtx.Request.SetHeader(contentLengthHeader, strconv.Itoa(len(requestBodyBytes)))
	}

	// Notify the data layer of the incoming request after headers are fully formed.
	s.eventNotifier.Notify(datasource.Event{
		Type:    datasource.RequestEventType,
		Payload: datasource.RequestPayload{Request: reqCtx.Request},
	})

	metrics.RecordSuccessCounter()
	reqCtx.RequestSentTimestamp = time.Now()

	ret = append(ret, &eppb.ProcessingResponse{
		Response: &eppb.ProcessingResponse_RequestHeaders{
			RequestHeaders: &eppb.HeadersResponse{
				Response: &eppb.CommonResponse{
					ClearRouteCache: true,
					HeaderMutation: &eppb.HeaderMutation{
						SetHeaders:    envoy.GenerateHeadersMutation(reqCtx.Request.MutatedHeaders()),
						RemoveHeaders: reqCtx.Request.RemovedHeaders(),
					},
				},
			},
		},
	})
	if bodyMutated {
		ret = addStreamedBodyResponse(ret, mutatedBodyBytes)
	} else {
		ret = addStreamedBodyResponse(ret, requestBodyBytes)
	}
	return ret, nil
}

// runRequestPlugins executes request plugins in the order they were registered.
func (s *Server) runRequestPlugins(ctx context.Context, cycleState *plugin.CycleState, request *requesthandling.InferenceRequest,
	reqPlugins []requesthandling.RequestProcessor) error {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)

	// Cache verbose logger and check Enabled() once to avoid per-iteration
	// allocations from argument boxing when logging at that level is disabled.
	verboseLogger := logger.V(logutil.VERBOSE)
	verboseEnabled := verboseLogger.Enabled()

	for _, reqPlugin := range reqPlugins {
		if verboseEnabled {
			verboseLogger.Info("Executing request plugin", "plugin", reqPlugin.TypedName())
		}
		before := time.Now()
		err := reqPlugin.ProcessRequest(ctx, cycleState, request)
		metrics.RecordPluginProcessingLatency(requestPluginExtensionPoint, reqPlugin.TypedName().Type, reqPlugin.TypedName().Name, time.Since(before))
		if err != nil {
			logger.Error(err, "Failed to execute request plugin", "plugin", reqPlugin.TypedName())
			return err
		}
	}

	return nil
}

func addStreamedBodyResponse(responses []*eppb.ProcessingResponse, requestBodyBytes []byte) []*eppb.ProcessingResponse {
	commonResponses := envoy.BuildChunkedBodyResponses(requestBodyBytes, true)
	for _, commonResp := range commonResponses {
		responses = append(responses, &eppb.ProcessingResponse{
			Response: &eppb.ProcessingResponse_RequestBody{
				RequestBody: &eppb.BodyResponse{
					Response: commonResp,
				},
			},
		})
	}
	return responses
}

// HandleRequestTrailers handles request trailers.
func (s *Server) HandleRequestTrailers(trailers *eppb.HttpTrailers) ([]*eppb.ProcessingResponse, error) {
	return []*eppb.ProcessingResponse{
		{
			Response: &eppb.ProcessingResponse_RequestTrailers{
				RequestTrailers: &eppb.TrailersResponse{},
			},
		},
	}, nil
}

// stripStreamOptions removes "stream_options":{...} from JSON body bytes.
func stripStreamOptions(body []byte) []byte {
	idx := bytes.Index(body, []byte(`"stream_options"`))
	if idx < 0 {
		return body
	}
	// Find the comma before or after and the closing brace
	start := idx
	// Walk back to find the preceding comma
	for start > 0 && body[start-1] != ',' {
		start--
	}
	if start > 0 && body[start-1] == ',' {
		start-- // include the comma
	}
	// Find the closing brace of stream_options value
	end := idx + len(`"stream_options"`)
	// Skip whitespace and colon
	for end < len(body) && (body[end] == ' ' || body[end] == ':') {
		end++
	}
	if end < len(body) && body[end] == '{' {
		depth := 1
		end++
		for end < len(body) && depth > 0 {
			if body[end] == '{' {
				depth++
			} else if body[end] == '}' {
				depth--
			}
			end++
		}
	}
	// Remove trailing comma if present
	for end < len(body) && body[end] == ',' {
		end++
	}
	result := make([]byte, 0, len(body))
	result = append(result, body[:start]...)
	result = append(result, body[end:]...)
	return result
}
