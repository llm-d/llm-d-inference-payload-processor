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
	"strconv"
	"testing"

	basepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/testing/protocmp"

	envoytest "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/envoy/test"
	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	datasource "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/basemodelextractor"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/bodyfieldtoheader"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/profilepicker/single"
	"github.com/llm-d/llm-d-inference-payload-processor/test/utils"
)

const testProfileName = "default"

func TestHandleRequestBody(t *testing.T) {
	ctx := logutil.NewTestLoggerIntoContext(context.Background())

	b, _ := json.Marshal(map[string]any{"model": "foo"})
	want := []*extProcPb.ProcessingResponse{
		{
			Response: &extProcPb.ProcessingResponse_RequestHeaders{
				RequestHeaders: &extProcPb.HeadersResponse{
					Response: &extProcPb.CommonResponse{
						ClearRouteCache: true,
						HeaderMutation: &extProcPb.HeaderMutation{
							SetHeaders: []*basepb.HeaderValueOption{
								{
									Header: &basepb.HeaderValue{
										Key:      contentLengthHeader,
										RawValue: []byte(strconv.Itoa(len(b))),
									},
								},
								{
									Header: &basepb.HeaderValue{
										Key:      bodyfieldtoheader.ModelHeader,
										RawValue: []byte("foo"),
									},
								},
								{
									Header: &basepb.HeaderValue{
										Key:      basemodelextractor.BaseModelHeader,
										RawValue: []byte(""),
									},
								},
							},
						},
					},
				},
			},
		},
		{
			Response: &extProcPb.ProcessingResponse_RequestBody{
				RequestBody: &extProcPb.BodyResponse{
					Response: &extProcPb.CommonResponse{
						BodyMutation: &extProcPb.BodyMutation{
							Mutation: &extProcPb.BodyMutation_StreamedResponse{
								StreamedResponse: &extProcPb.StreamedBodyResponse{
									Body:        b,
									EndOfStream: true,
								},
							},
						},
					},
				},
			},
		},
	}

	baseModelToHeaderPlugin := &basemodelextractor.BaseModelToHeaderPlugin{AdaptersStore: basemodelextractor.NewAdaptersStore()}
	modelToHeaderPlugin, _ := bodyfieldtoheader.NewBodyFieldToHeaderPlugin(modelField, bodyfieldtoheader.ModelHeader)
	profiles := newTestProfiles()
	addRequestPlugins(profiles, modelToHeaderPlugin, baseModelToHeaderPlugin)
	srv := newServerForTest(profiles)
	reqCtx := &RequestContext{
		CycleState: plugin.NewCycleState(),
		Request:    requesthandling.NewInferenceRequest(),
	}
	got, err := srv.HandleRequestBody(ctx, reqCtx, b)
	if err != nil {
		t.Fatalf("HandleRequestBody(): %v", err)
	}

	// sort headers in responses for deterministic tests
	envoytest.SortSetHeadersInResponses(want)
	envoytest.SortSetHeadersInResponses(got)
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("HandleRequestBody returned unexpected response, diff(-want, +got): %v", diff)
	}
}

func TestHandleResponseBody_Streaming(t *testing.T) {
	ctx := logutil.NewTestLoggerIntoContext(context.Background())
	wantFullBody := []byte(`{"choices":[{"text":"Hello!"}]}`)

	profiles := newTestProfiles()
	ref := newServerForTest(profiles)
	want, err := ref.HandleResponseBody(ctx, newTestRequestContext(profiles), wantFullBody)
	if err != nil {
		t.Fatalf("reference HandleResponseBody: %v", err)
	}

	type chunk struct {
		body        []byte
		endOfStream bool
	}
	tests := []struct {
		name   string
		chunks []chunk
	}{
		{
			name: "single chunk with EoS",
			chunks: []chunk{
				{body: wantFullBody, endOfStream: true},
			},
		},
		{
			name: "split JSON across two chunks, EoS on last",
			chunks: []chunk{
				{body: []byte(`{"choices":[{"te`), endOfStream: false},
				{body: []byte(`xt":"Hello!"}]}`), endOfStream: true},
			},
		},
		{
			name: "fragmented: three chunks, EoS on last",
			chunks: []chunk{
				{body: []byte(`{"choices":`), endOfStream: false},
				{body: []byte(`[{"text":"Hello!"}]`), endOfStream: false},
				{body: []byte(`}`), endOfStream: true},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			streamCtx, cancel := context.WithCancel(logutil.NewTestLoggerIntoContext(context.Background()))
			profiles := newTestProfiles()
			srv := newServerForTest(profiles)
			testListener, errChan := utils.SetupTestStreamingServer(t, streamCtx, srv)
			process, conn := utils.GetStreamingServerClient(streamCtx, t)
			defer conn.Close()
			defer func() {
				cancel()
				<-errChan
				testListener.Close()
			}()

			request := &extProcPb.ProcessingRequest{
				Request: &extProcPb.ProcessingRequest_RequestHeaders{},
			}
			if err := process.Send(request); err != nil {
				t.Fatalf("send request headers: %v", err)
			}

			request = &extProcPb.ProcessingRequest{
				Request: &extProcPb.ProcessingRequest_RequestBody{
					RequestBody: &extProcPb.HttpBody{
						Body:        []byte("{\"model\":\"testing\"}"),
						EndOfStream: true,
					},
				},
			}
			if err := process.Send(request); err != nil {
				t.Fatalf("send request body: %v", err)
			}

			msg, err := process.Recv()
			if err != nil {
				t.Fatalf("receive error: %v", err)
			}
			if msg.GetRequestHeaders() == nil {
				t.Fatalf("Didn't receive the Processing_Response for Request Headers")
			}

			msg, err = process.Recv()
			if err != nil {
				t.Fatalf("receive error: %v", err)
			}
			if msg.GetRequestBody() == nil {
				t.Fatalf("Didn't receive the Processing_Response for Request Body")
			}

			respHeaders := utils.BuildEnvoyGRPCHeaders(map[string]string{
				"x-test":       "body",
				":method":      "POST",
				"content-type": "text/event-stream",
			}, true)
			request = &extProcPb.ProcessingRequest{
				Request: &extProcPb.ProcessingRequest_ResponseHeaders{
					ResponseHeaders: respHeaders,
				},
			}
			if err := process.Send(request); err != nil {
				t.Fatalf("send response headers: %v", err)
			}

			for _, c := range tc.chunks {
				request = &extProcPb.ProcessingRequest{
					Request: &extProcPb.ProcessingRequest_ResponseBody{
						ResponseBody: &extProcPb.HttpBody{
							Body:        c.body,
							EndOfStream: c.endOfStream,
						},
					},
				}
				if err := process.Send(request); err != nil {
					t.Fatalf("send response body chunk: %v", err)
				}
			}

			got := make([]*extProcPb.ProcessingResponse, 0, len(want))
			for range want {
				msg, err := process.Recv()
				if err != nil {
					t.Fatalf("recv response phase: %v", err)
				}
				got = append(got, msg)
			}

			envoytest.SortSetHeadersInResponses(want)
			envoytest.SortSetHeadersInResponses(got)
			if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
				t.Errorf("unexpected ProcessingResponse after buffered streaming response body, diff(-want, +got): %s", diff)
			}
		})
	}
}

// recordingSink is a minimal logr.LogSink that records the key/value pairs added
// via WithValues so tests can assert on logger enrichment.
type recordingSink struct {
	values []any
}

func (s *recordingSink) Init(logr.RuntimeInfo)        {}
func (s *recordingSink) Enabled(int) bool             { return true }
func (s *recordingSink) Info(int, string, ...any)     {}
func (s *recordingSink) Error(error, string, ...any)  {}
func (s *recordingSink) WithName(string) logr.LogSink { return s }
func (s *recordingSink) WithValues(kv ...any) logr.LogSink {
	return &recordingSink{values: append(append([]any{}, s.values...), kv...)}
}

func TestLoggerWithSpanContext(t *testing.T) {
	traceID, err := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("TraceIDFromHex: %v", err)
	}
	spanID, err := trace.SpanIDFromHex("0123456789abcdef")
	if err != nil {
		t.Fatalf("SpanIDFromHex: %v", err)
	}

	tests := []struct {
		name    string
		sc      trace.SpanContext
		wantOK  bool
		wantKVs map[string]string
	}{
		{
			name: "valid span context is injected",
			sc: trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: traceID,
				SpanID:  spanID,
			}),
			wantOK: true,
			wantKVs: map[string]string{
				"trace_id": traceID.String(),
				"span_id":  spanID.String(),
			},
		},
		{
			name:   "invalid span context is left unchanged",
			sc:     trace.SpanContext{},
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sink := &recordingSink{}
			got, ok := loggerWithSpanContext(logr.New(sink), tc.sc)
			if ok != tc.wantOK {
				t.Fatalf("loggerWithSpanContext ok = %v, want %v", ok, tc.wantOK)
			}

			gotKVs := map[string]string{}
			for i := 0; i+1 < len(got.GetSink().(*recordingSink).values); i += 2 {
				k, _ := got.GetSink().(*recordingSink).values[i].(string)
				v, _ := got.GetSink().(*recordingSink).values[i+1].(string)
				gotKVs[k] = v
			}

			if !tc.wantOK {
				if len(gotKVs) != 0 {
					t.Fatalf("expected no values for invalid span context, got %v", gotKVs)
				}
				return
			}
			if diff := cmp.Diff(tc.wantKVs, gotKVs); diff != "" {
				t.Errorf("injected values mismatch, diff(-want, +got): %v", diff)
			}
		})
	}
}

type noopNotifier struct{}

func (noopNotifier) Notify(datasource.Event) {}

func newServerForTest(profiles map[string]*requesthandling.Profile) *Server {
	return NewServer(single.NewSingleProfilePicker(), profiles).WithEventNotifier(noopNotifier{})
}

func newTestProfiles() map[string]*requesthandling.Profile {
	return map[string]*requesthandling.Profile{
		testProfileName: {
			RequestPlugins:  []requesthandling.RequestProcessor{},
			ResponsePlugins: []requesthandling.ResponseProcessor{},
		},
	}
}

func addRequestPlugins(p map[string]*requesthandling.Profile, plugins ...requesthandling.RequestProcessor) {
	p[testProfileName].RequestPlugins = plugins
}

func withResponsePlugins(p map[string]*requesthandling.Profile, plugins ...requesthandling.ResponseProcessor) {
	p[testProfileName].ResponsePlugins = plugins
}
