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
	"errors"
	"io"
	"time"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"sigs.k8s.io/controller-runtime/pkg/log"

	envoy "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/envoy"
	errcommon "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/error"
	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	datasource "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/metrics"
	"github.com/llm-d/llm-d-inference-payload-processor/version"
)

const (
	contentLengthHeader = "Content-Length"
	requestIdHeaderKey  = "x-request-id"

	requestPluginExtensionPoint  = "request"
	responsePluginExtensionPoint = "response"
)

func NewServer(preProcessors []requesthandling.RequestProcessor, profilePicker requesthandling.ProfilePicker,
	profiles map[string]*requesthandling.Profile, postProcessors []requesthandling.ResponseProcessor,
	responseHeadersPostProcessors []requesthandling.ResponseHeadersProcessor) *Server {
	return &Server{
		preProcessors:                 preProcessors,
		profilePicker:                 profilePicker,
		profiles:                      profiles,
		postProcessors:                postProcessors,
		responseHeadersPostProcessors: responseHeadersPostProcessors,
		emptyProfile:                  requesthandling.NewProfile(),
	}
}

// WithEventNotifier sets the event notifier used to feed the data layer.
func (s *Server) WithEventNotifier(n datasource.EventNotifier) *Server {
	s.eventNotifier = n
	return s
}

// Server implements the Envoy external processing server.
// https://www.envoyproxy.io/docs/envoy/latest/api-v3/service/ext_proc/v3/external_processor.proto
type Server struct {
	preProcessors                 []requesthandling.RequestProcessor
	profilePicker                 requesthandling.ProfilePicker
	profiles                      map[string]*requesthandling.Profile
	postProcessors                []requesthandling.ResponseProcessor
	responseHeadersPostProcessors []requesthandling.ResponseHeadersProcessor
	eventNotifier                 datasource.EventNotifier

	emptyProfile *requesthandling.Profile
}

// RequestContext stores context information during the lifetime of an HTTP request.
type RequestContext struct {
	RequestReceivedTimestamp    time.Time
	RequestSentTimestamp        time.Time
	ResponseFirstChunkTimestamp time.Time
	ResponseCompleteTimestamp   time.Time
	ResponseHeadersSent         bool
	Profile                     *requesthandling.Profile
	CycleState                  *plugin.CycleState
	Request                     *requesthandling.InferenceRequest
	Response                    *requesthandling.InferenceResponse
}

// loggerWithSpanContext returns a copy of logger enriched with the trace_id and
// span_id of sc so log lines can be correlated with the corresponding trace. The
// returned bool reports whether sc was valid and enrichment was applied.
func loggerWithSpanContext(logger logr.Logger, sc trace.SpanContext) (logr.Logger, bool) {
	if !sc.IsValid() {
		return logger, false
	}
	return logger.WithValues(
		"trace_id", sc.TraceID().String(),
		"span_id", sc.SpanID().String(),
	), true
}

func (s *Server) Process(srv extProcPb.ExternalProcessor_ProcessServer) error {
	ctx := srv.Context()

	// Start tracing span for the request
	tracer := otel.Tracer(
		"llm-d-inference-payload-processor/pkg/handlers",
		trace.WithInstrumentationVersion(version.BuildRef),
		trace.WithInstrumentationAttributes(
			attribute.String("commit-sha", version.CommitSHA),
		),
	)
	ctx, span := tracer.Start(ctx, "gateway.request", trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()

	logger := log.FromContext(ctx)
	// Correlate logs with traces: enrich the request logger with the active
	// span's trace_id/span_id and store it back into the context so every
	// downstream log line (handlers, plugins, model selector) can be queried
	// by trace_id alongside the OpenTelemetry spans.
	if enriched, ok := loggerWithSpanContext(logger, span.SpanContext()); ok {
		logger = enriched
		ctx = log.IntoContext(ctx, logger)
	}
	loggerVerbose := logger.V(logutil.VERBOSE)
	loggerVerbose.Info("Processing")

	reqCtx := &RequestContext{
		Request:    requesthandling.NewInferenceRequest(),
		Response:   requesthandling.NewInferenceResponse(),
		Profile:    s.emptyProfile, // request is always initialized with an empty profile to avoid nil pointer
		CycleState: plugin.NewCycleState(),
	}
	// TODO set a max cap on these.
	// both requestBody and responseBody accumulate without an upper bound.
	// An arbitrarily large body can OOM the code.
	var requestBody []byte
	var responseBody []byte
	// Envoy can re-deliver the final body chunk after EndOfStream in
	// FULL_DUPLEX_STREAMED mode (observed with >1MB bodies on Envoy 1.35+).
	// Track completion so duplicates are ignored instead of being appended
	// to the already-processed buffer, which corrupts it.
	requestBodyComplete := false
	responseBodyComplete := false

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		req, recvErr := srv.Recv()
		if recvErr == io.EOF || errors.Is(recvErr, context.Canceled) {
			return nil
		}
		if recvErr != nil {
			return status.Errorf(codes.Unknown, "cannot receive stream request: %v", recvErr)
		}

		var responses []*extProcPb.ProcessingResponse
		var err error
		switch v := req.Request.(type) {
		case *extProcPb.ProcessingRequest_RequestHeaders:
			if requestId := envoy.ExtractHeaderValue(v, requestIdHeaderKey); len(requestId) > 0 {
				logger = logger.WithValues(requestIdHeaderKey, requestId)
				loggerVerbose = logger.V(logutil.VERBOSE)
				ctx = log.IntoContext(ctx, logger)
			}
			responses = s.HandleRequestHeaders(ctx, reqCtx, v.RequestHeaders)
			loggerVerbose.Info("processing request headers complete")
		case *extProcPb.ProcessingRequest_RequestBody:
			loggerVerbose.Info("Incoming request body chunk", "EoS", v.RequestBody.EndOfStream)
			if requestBodyComplete {
				loggerVerbose.Info("ignoring request body chunk delivered after EndOfStream")
				continue
			}
			requestBody = append(requestBody, v.RequestBody.Body...)
			if !v.RequestBody.EndOfStream {
				continue
			}
			requestBodyComplete = true
			responses, err = s.HandleRequestBody(ctx, reqCtx, requestBody)
			loggerVerbose.Info("processing request body complete")
		case *extProcPb.ProcessingRequest_RequestTrailers:
			responses, err = s.HandleRequestTrailers(v.RequestTrailers)
		case *extProcPb.ProcessingRequest_ResponseHeaders:
			responses, err = s.HandleResponseHeaders(ctx, reqCtx, v.ResponseHeaders)
			loggerVerbose.Info("processing response headers complete")
		case *extProcPb.ProcessingRequest_ResponseBody:
			loggerVerbose.Info("Incoming response body chunk", "EoS", v.ResponseBody.EndOfStream)
			if responseBodyComplete {
				loggerVerbose.Info("ignoring response body chunk delivered after EndOfStream")
				continue
			}
			if reqCtx.ResponseFirstChunkTimestamp.IsZero() {
				reqCtx.ResponseFirstChunkTimestamp = time.Now()
			}

			if reqCtx.Profile.NeedsResponseBuffering {
				responseBody = append(responseBody, v.ResponseBody.Body...)
				if !v.ResponseBody.EndOfStream {
					// Keep accumulating — don't send responses or record metrics yet.
					break
				}
				responses, err = s.HandleResponseBody(ctx, reqCtx, responseBody)
				loggerVerbose.Info("processing response body complete")
			} else {
				responses, err = s.HandleResponseChunk(ctx, reqCtx, v.ResponseBody.Body, v.ResponseBody.EndOfStream)
				loggerVerbose.Info("response chunk processing complete")
			}

			if v.ResponseBody.EndOfStream {
				responseBodyComplete = true
				reqCtx.ResponseCompleteTimestamp = time.Now()
				model, _ := reqCtx.Request.Body["model"].(string)
				metrics.RecordRequestTTFT(model, reqCtx.ResponseFirstChunkTimestamp.Sub(reqCtx.RequestReceivedTimestamp))

				// Notify the data layer of the completed response.
				s.eventNotifier.Notify(datasource.Event{
					Type: datasource.ResponseEventType,
					Payload: datasource.ResponsePayload{
						Request:    reqCtx.Request,
						Response:   reqCtx.Response,
						CycleState: reqCtx.CycleState,
						Duration:   reqCtx.ResponseCompleteTimestamp.Sub(reqCtx.RequestReceivedTimestamp),
						TTFT:       reqCtx.ResponseFirstChunkTimestamp.Sub(reqCtx.RequestSentTimestamp),
					},
				})
			}
		case *extProcPb.ProcessingRequest_ResponseTrailers:
			responses, err = s.HandleResponseTrailers(v.ResponseTrailers)
		default:
			logger.Error(nil, "unknown Request type", "request", v)
			return status.Error(codes.Unknown, "unknown request type")
		}

		// Handle the err and fire an immediate response.
		if err != nil {
			if logger.V(logutil.DEBUG).Enabled() {
				logger.V(logutil.DEBUG).Error(err, "failed to process request", "request", req)
			} else {
				logger.Error(err, "failed to process request")
			}
			resp, err := errcommon.BuildErrResponse(err)
			if err != nil {
				return err
			}
			if sendErr := srv.Send(resp); sendErr != nil {
				logger.Error(sendErr, "Send failed")
				return status.Errorf(codes.Unknown, "failed to send response back to Envoy: %v", sendErr)
			}
			return nil
		}

		for _, resp := range responses {
			if err := srv.Send(resp); err != nil {
				logger.Error(err, "send failed")
				return status.Errorf(codes.Unknown, "failed to send response back to Envoy: %v", err)
			}
		}
	}
}
