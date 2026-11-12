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
	"errors"
	"strconv"
	"testing"

	basepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"

	envoytest "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/envoy/test"
	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

const testPluginValue = "done"

// fakeResponsePlugin implements requesthandling.PayloadProcessor for testing response plugin execution.
type fakeResponsePlugin struct {
	name     string
	mutateFn func(ctx context.Context, cycleState *plugin.CycleState, response *requesthandling.InferenceResponse) error
}

func (p *fakeResponsePlugin) TypedName() plugin.TypedName {
	return plugin.TypedName{Type: "fake", Name: p.name}
}

func (p *fakeResponsePlugin) ProcessResponse(ctx context.Context, cycleState *plugin.CycleState, response *requesthandling.InferenceResponse) error {
	return p.mutateFn(ctx, cycleState, response)
}

var _ requesthandling.ResponseProcessor = &fakeResponsePlugin{}

func newTestRequestContext(profiles map[string]*requesthandling.Profile) *RequestContext {
	return &RequestContext{
		Profile:    profiles[testProfileName],
		CycleState: plugin.NewCycleState(),
		Request:    requesthandling.NewInferenceRequest(),
		Response:   requesthandling.NewInferenceResponse(),
	}
}

func TestHandleResponseBody_NoPlugins(t *testing.T) {
	ctx := logutil.NewTestLoggerIntoContext(context.Background())

	profiles := newTestProfiles()
	server := newServerForTest(profiles)
	responseBody := []byte(`{"choices":[{"text":"Hello!"}]}`)
	resp, err := server.HandleResponseBody(ctx, newTestRequestContext(profiles), responseBody)
	if err != nil {
		t.Fatalf("HandleResponseBody returned unexpected error: %v", err)
	}

	want := []*extProcPb.ProcessingResponse{
		{
			Response: &extProcPb.ProcessingResponse_ResponseHeaders{
				ResponseHeaders: &extProcPb.HeadersResponse{},
			},
		},
		{
			Response: &extProcPb.ProcessingResponse_ResponseBody{
				ResponseBody: &extProcPb.BodyResponse{
					Response: &extProcPb.CommonResponse{
						BodyMutation: &extProcPb.BodyMutation{
							Mutation: &extProcPb.BodyMutation_StreamedResponse{
								StreamedResponse: &extProcPb.StreamedBodyResponse{
									Body:        responseBody,
									EndOfStream: true,
								},
							},
						},
					},
				},
			},
		},
	}

	if diff := cmp.Diff(want, resp, protocmp.Transform()); diff != "" {
		t.Errorf("HandleResponseBody returned unexpected response, diff(-want, +got): %v", diff)
	}
}

func TestHandleResponseBody_SinglePlugin(t *testing.T) {
	ctx := logutil.NewTestLoggerIntoContext(context.Background())

	mutatePlugin := &fakeResponsePlugin{
		name: "mutator",
		mutateFn: func(_ context.Context, _ *plugin.CycleState, response *requesthandling.InferenceResponse) error {
			response.SetBodyField("mutated", true)
			return nil
		},
	}

	profiles := newTestProfiles()
	withResponsePlugins(profiles, mutatePlugin)
	server := newServerForTest(profiles)
	responseBody := []byte(`{"choices":[{"text":"Hello!"}]}`)
	resp, err := server.HandleResponseBody(ctx, newTestRequestContext(profiles), responseBody)
	if err != nil {
		t.Fatalf("HandleResponseBody returned unexpected error: %v", err)
	}

	wantBody, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"text": "Hello!"}},
		"mutated": true,
	})
	want := expectedStreamedResponseBodyMutation(wantBody)

	envoytest.SortSetHeadersInResponses(want)
	envoytest.SortSetHeadersInResponses(resp)
	if diff := cmp.Diff(want, resp, protocmp.Transform()); diff != "" {
		t.Errorf("HandleResponseBody returned unexpected response, diff(-want, +got): %v", diff)
	}
}

func TestHandleResponseBody_MultiplePlugins(t *testing.T) {
	ctx := logutil.NewTestLoggerIntoContext(context.Background())

	plugin1 := &fakeResponsePlugin{
		name: "plugin1",
		mutateFn: func(_ context.Context, _ *plugin.CycleState, response *requesthandling.InferenceResponse) error {
			response.SetBodyField("p1", testPluginValue)
			return nil
		},
	}
	plugin2 := &fakeResponsePlugin{
		name: "plugin2",
		mutateFn: func(_ context.Context, _ *plugin.CycleState, response *requesthandling.InferenceResponse) error {
			response.SetBodyField("p2", testPluginValue)
			return nil
		},
	}

	profiles := newTestProfiles()
	withResponsePlugins(profiles, plugin1, plugin2)
	server := newServerForTest(profiles)
	responseBody := []byte(`{"original":true}`)
	resp, err := server.HandleResponseBody(ctx, newTestRequestContext(profiles), responseBody)
	if err != nil {
		t.Fatalf("HandleResponseBody returned unexpected error: %v", err)
	}

	wantBody, _ := json.Marshal(map[string]any{
		"original": true,
		"p1":       testPluginValue,
		"p2":       testPluginValue,
	})
	want := expectedStreamedResponseBodyMutation(wantBody)

	envoytest.SortSetHeadersInResponses(want)
	envoytest.SortSetHeadersInResponses(resp)
	if diff := cmp.Diff(want, resp, protocmp.Transform()); diff != "" {
		t.Errorf("HandleResponseBody returned unexpected response, diff(-want, +got): %v", diff)
	}
}

func TestHandleResponseBody_PluginError(t *testing.T) {
	ctx := logutil.NewTestLoggerIntoContext(context.Background())

	failingPlugin := &fakeResponsePlugin{
		name: "failing",
		mutateFn: func(_ context.Context, _ *plugin.CycleState, _ *requesthandling.InferenceResponse) error {
			return errors.New("failed to execute plugin")
		},
	}

	profiles := newTestProfiles()
	withResponsePlugins(profiles, failingPlugin)
	server := newServerForTest(profiles)
	responseBody := []byte(`{"choices":[{"text":"some response"}]}`)
	_, err := server.HandleResponseBody(ctx, newTestRequestContext(profiles), responseBody)
	if err == nil {
		t.Fatal("HandleResponseBody should have returned an error")
	}

	if got := err.Error(); got == "" {
		t.Error("Expected non-empty error message")
	}
}

func TestHandleResponseBody_StreamingWithPlugin(t *testing.T) {
	ctx := logutil.NewTestLoggerIntoContext(context.Background())

	mutatePlugin := &fakeResponsePlugin{
		name: "mutator",
		mutateFn: func(_ context.Context, _ *plugin.CycleState, response *requesthandling.InferenceResponse) error {
			response.SetBodyField("mutated", true)
			return nil
		},
	}

	profiles := newTestProfiles()
	withResponsePlugins(profiles, mutatePlugin)
	server := newServerForTest(profiles)
	responseBody := []byte(`{"choices":[{"text":"Hello!"}]}`)
	resp, err := server.HandleResponseBody(ctx, newTestRequestContext(profiles), responseBody)
	if err != nil {
		t.Fatalf("HandleResponseBody returned unexpected error: %v", err)
	}

	wantBody, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"text": "Hello!"}},
		"mutated": true,
	})
	want := expectedStreamedResponseBodyMutation(wantBody)

	envoytest.SortSetHeadersInResponses(want)
	envoytest.SortSetHeadersInResponses(resp)
	if diff := cmp.Diff(want, resp, protocmp.Transform()); diff != "" {
		t.Errorf("HandleResponseBody returned unexpected response, diff(-want, +got): %v", diff)
	}
}

func TestHandleResponseBody_PluginNoBodyMutation(t *testing.T) {
	ctx := logutil.NewTestLoggerIntoContext(context.Background())

	headerOnlyPlugin := &fakeResponsePlugin{
		name: "header-only",
		mutateFn: func(_ context.Context, _ *plugin.CycleState, response *requesthandling.InferenceResponse) error {
			response.SetHeader("X-Custom-Response", "added")
			return nil
		},
	}

	responseBody := []byte(`{"choices":[{"text":"Hello!"}]}`)
	want := []*extProcPb.ProcessingResponse{
		{
			Response: &extProcPb.ProcessingResponse_ResponseHeaders{
				ResponseHeaders: &extProcPb.HeadersResponse{
					Response: &extProcPb.CommonResponse{
						ClearRouteCache: true,
						HeaderMutation: &extProcPb.HeaderMutation{
							SetHeaders: []*basepb.HeaderValueOption{
								{
									Header: &basepb.HeaderValue{
										Key:      "X-Custom-Response",
										RawValue: []byte("added"),
									},
									AppendAction: basepb.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
								},
							},
						},
					},
				},
			},
		},
		{
			Response: &extProcPb.ProcessingResponse_ResponseBody{
				ResponseBody: &extProcPb.BodyResponse{
					Response: &extProcPb.CommonResponse{
						BodyMutation: &extProcPb.BodyMutation{
							Mutation: &extProcPb.BodyMutation_StreamedResponse{
								StreamedResponse: &extProcPb.StreamedBodyResponse{
									Body:        responseBody,
									EndOfStream: true,
								},
							},
						},
					},
				},
			},
		},
	}

	profiles := newTestProfiles()
	withResponsePlugins(profiles, headerOnlyPlugin)
	server := newServerForTest(profiles)
	resp, err := server.HandleResponseBody(ctx, newTestRequestContext(profiles), responseBody)
	if err != nil {
		t.Fatalf("HandleResponseBody returned unexpected error: %v", err)
	}

	envoytest.SortSetHeadersInResponses(want)
	envoytest.SortSetHeadersInResponses(resp)
	if diff := cmp.Diff(want, resp, protocmp.Transform()); diff != "" {
		t.Errorf("HandleResponseBody returned unexpected response, diff(-want, +got): %v", diff)
	}
}

// expectedStreamedResponseBodyMutation builds the expected streamed response for a mutated body:
// a deferred ResponseHeaders with header mutation, then a ResponseBody with StreamedBodyResponse.
func expectedStreamedResponseBodyMutation(bodyBytes []byte) []*extProcPb.ProcessingResponse {
	return []*extProcPb.ProcessingResponse{
		{
			Response: &extProcPb.ProcessingResponse_ResponseHeaders{
				ResponseHeaders: &extProcPb.HeadersResponse{
					Response: &extProcPb.CommonResponse{
						ClearRouteCache: true,
						HeaderMutation: &extProcPb.HeaderMutation{
							SetHeaders: []*basepb.HeaderValueOption{
								{
									Header: &basepb.HeaderValue{
										Key:      contentLengthHeader,
										RawValue: []byte(strconv.Itoa(len(bodyBytes))),
									},
									AppendAction: basepb.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
								},
							},
						},
					},
				},
			},
		},
		{
			Response: &extProcPb.ProcessingResponse_ResponseBody{
				ResponseBody: &extProcPb.BodyResponse{
					Response: &extProcPb.CommonResponse{
						BodyMutation: &extProcPb.BodyMutation{
							Mutation: &extProcPb.BodyMutation_StreamedResponse{
								StreamedResponse: &extProcPb.StreamedBodyResponse{
									Body:        bodyBytes,
									EndOfStream: true,
								},
							},
						},
					},
				},
			},
		},
	}
}

func TestParseSSEResponseBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want map[string]any
	}{
		{
			name: "openai chat completion chunks rebuild choices",
			body: "data: {\"model\":\"gpt\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"}}]}\n\n" +
				"data: {\"model\":\"gpt\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hel\"}}]}\n\n" +
				"data: {\"model\":\"gpt\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: {\"model\":\"gpt\",\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":2}}\n\n" +
				"data: [DONE]\n\n",
			want: map[string]any{
				"model": "gpt",
				"choices": []any{
					map[string]any{
						"index":         0,
						"message":       map[string]any{"role": "assistant", "content": "Hello"},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]any{"prompt_tokens": float64(9), "completion_tokens": float64(2)},
			},
		},
		{
			name: "openai chat completion chunks keep each choice apart",
			body: "data: {\"model\":\"gpt\",\"choices\":[{\"index\":1,\"delta\":{\"content\":\"b\"}},{\"index\":0,\"delta\":{\"content\":\"a\"}}]}\n\n" +
				"data: {\"model\":\"gpt\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"length\"},{\"index\":1,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n",
			want: map[string]any{
				"model": "gpt",
				"choices": []any{
					map[string]any{
						"index":         0,
						"message":       map[string]any{"role": "assistant", "content": "a"},
						"finish_reason": "length",
					},
					map[string]any{
						"index":         1,
						"message":       map[string]any{"role": "assistant", "content": "b"},
						"finish_reason": "stop",
					},
				},
			},
		},
		{
			name: "openai chat completion without content leaves content null",
			body: "data: {\"model\":\"gpt\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":\"tool_calls\"}]}\n\n",
			want: map[string]any{
				"model": "gpt",
				"choices": []any{
					map[string]any{
						"index":         0,
						"message":       map[string]any{"role": "assistant", "content": nil},
						"finish_reason": "tool_calls",
					},
				},
			},
		},
		{
			name: "anthropic message keeps input tokens from message_start",
			body: "event: message_start\n" +
				"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude\",\"usage\":{\"input_tokens\":25,\"cache_read_input_tokens\":3,\"output_tokens\":1}}}\n\n" +
				"event: content_block_delta\n" +
				"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\n" +
				"event: content_block_delta\n" +
				"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\" there\"}}\n\n" +
				"event: message_delta\n" +
				"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":15}}\n\n" +
				"event: message_stop\n" +
				"data: {\"type\":\"message_stop\"}\n\n",
			want: map[string]any{
				"model":       "claude",
				"type":        "message",
				"role":        "assistant",
				"content":     []any{map[string]any{"type": "text", "text": "Hi there"}},
				"stop_reason": "end_turn",
				"usage": map[string]any{
					"input_tokens":            float64(25),
					"cache_read_input_tokens": float64(3),
					"output_tokens":           float64(15),
				},
			},
		},
		{
			name: "openai responses usage nested in response",
			body: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hi\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt\",\"usage\":{\"input_tokens\":4,\"output_tokens\":1}}}\n\n",
			want: map[string]any{
				"model": "gpt",
				"usage": map[string]any{"input_tokens": float64(4), "output_tokens": float64(1)},
			},
		},
		{
			name: "no data lines",
			body: "event: ping\n\n",
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSSEResponseBody([]byte(tc.body))
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("parseSSEResponseBody returned unexpected body, diff(-want, +got): %v", diff)
			}
		})
	}
}
