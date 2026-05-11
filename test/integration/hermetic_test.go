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

// Package payloadprocessor contains integration tests for the payload processor extension.
package integration

import (
	"context"
	"strings"
	"testing"

	envoyCorev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	envoyTypePb "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/testing/protocmp"

	envoytest "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/envoy/test"
	"sigs.k8s.io/gateway-api-inference-extension/test/integration"
)

// TestBodyBasedRouting validates the full-duplex streaming behavior of the payload processor.
// This validates that the payload processor correctly buffers streamed chunks, inspects the body, and injects the header.
func TestBodyBasedRouting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		reqs             []*extProcPb.ProcessingRequest
		wantResponses    []*extProcPb.ProcessingResponse
		wantErr          bool
		wantStatusCode   envoyTypePb.StatusCode
		wantBodyContains string
	}{
		{
			name: "success: adds model header from simple body",
			reqs: integration.ReqLLM(logger, "test", "foo", "bar"),
			wantResponses: []*extProcPb.ProcessingResponse{
				ExpectHeader("foo", "qwen", "64"),
				ExpectBodyPassThrough("test", "foo"),
			},
		},
		{
			name: "success: buffers split chunks and extracts model",
			reqs: integration.ReqRaw(
				map[string]string{"hi": "mom"},
				`{"max_tokens":100,"model":"sql-lo`,
				`ra-sheddable","prompt":"test","temperature":0}`,
			),
			wantResponses: []*extProcPb.ProcessingResponse{
				ExpectHeader("sql-lora-sheddable", "qwen", "79"),
				ExpectBodyPassThrough("test", "sql-lora-sheddable"),
			},
		},
		{
			name: "missing model field - skips gracefully",
			reqs: integration.ReqLLM(logger, "test", "", ""),
			wantResponses: []*extProcPb.ProcessingResponse{
				{
					Response: &extProcPb.ProcessingResponse_RequestHeaders{
						RequestHeaders: &extProcPb.HeadersResponse{
							Response: &extProcPb.CommonResponse{
								ClearRouteCache: true,
								HeaderMutation: &extProcPb.HeaderMutation{
									SetHeaders: []*envoyCorev3.HeaderValueOption{
										{
											Header: &envoyCorev3.HeaderValue{
												Key:      "Content-Length",
												RawValue: []byte("50"),
											},
										},
									},
								},
							},
						},
					},
				},
				ExpectBodyPassThrough("test", ""),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			h := NewHarness(t, ctx)

			expectedCount := len(tc.wantResponses)
			if tc.wantStatusCode != 0 || tc.wantErr {
				expectedCount = 1
			}

			responses, err := integration.StreamedRequest(t, h.Client, tc.reqs, expectedCount)

			if tc.wantErr {
				require.Error(t, err, "expected stream error")
				return
			}
			require.NoError(t, err, "unexpected stream error")

			if tc.wantStatusCode != 0 {
				require.Len(t, responses, 1)
				ir := responses[0].GetImmediateResponse()
				require.NotNil(t, ir, "expected ImmediateResponse")
				require.Equal(t, tc.wantStatusCode, ir.GetStatus().GetCode())
				if tc.wantBodyContains != "" {
					require.True(t, strings.Contains(string(ir.GetBody()), tc.wantBodyContains),
						"ImmediateResponse body %s should contain %s", string(ir.GetBody()), tc.wantBodyContains)
				}
				return
			}

			// sort headers in responses for deterministic tests
			envoytest.SortSetHeadersInResponses(tc.wantResponses)
			envoytest.SortSetHeadersInResponses(responses)
			if diff := cmp.Diff(tc.wantResponses, responses, protocmp.Transform()); diff != "" {
				t.Errorf("Response mismatch (-want +got): %v", diff)
			}
		})
	}
}
