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

package integration

import (
	"encoding/json"

	envoyCorev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

// --- Response Expectations (Streaming) ---

// ExpectHeader asserts that the payload processor set the specific model header and cleared the route cache.
// baseModelName is the expected base model name (e.g., "qwen" for both "qwen" and "sql-lora-sheddable")
func ExpectHeader(modelName, baseModelName string, contentLength string) *extProcPb.ProcessingResponse {
	return &extProcPb.ProcessingResponse{
		Response: &extProcPb.ProcessingResponse_RequestHeaders{
			RequestHeaders: &extProcPb.HeadersResponse{
				Response: &extProcPb.CommonResponse{
					ClearRouteCache: true,
					HeaderMutation: &extProcPb.HeaderMutation{
						SetHeaders: []*envoyCorev3.HeaderValueOption{
							{
								Header: &envoyCorev3.HeaderValue{
									Key:      "Content-Length",
									RawValue: []byte(contentLength),
								},
							},
							{
								Header: &envoyCorev3.HeaderValue{
									Key:      "X-Gateway-Base-Model-Name",
									RawValue: []byte(baseModelName),
								},
							},
							{
								Header: &envoyCorev3.HeaderValue{
									Key:      "X-Gateway-Model-Name",
									RawValue: []byte(modelName),
								},
							},
						},
					},
				},
			},
		},
	}
}

// ExpectBodyPassThrough asserts that the payload processor reconstructs and passes the body through.
// The payload processor buffers the body to inspect it, then sends it downstream as a single chunk (usually).
func ExpectBodyPassThrough(prompt, model string) *extProcPb.ProcessingResponse {
	j := map[string]any{
		"max_tokens": 100, "prompt": prompt, "temperature": 0,
	}
	if model != "" {
		j["model"] = model
	}
	b, _ := json.Marshal(j)

	return &extProcPb.ProcessingResponse{
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
	}
}
