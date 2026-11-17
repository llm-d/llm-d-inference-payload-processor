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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/status"
	"sigs.k8s.io/gateway-api-inference-extension/test/integration"

	runserver "github.com/llm-d/llm-d-inference-payload-processor/pkg/server"
)

// sendAndCollectBody streams the requests and reassembles the mutated request body
// from the streamed responses until end of stream or a stream error.
func sendAndCollectBody(t *testing.T, client extProcPb.ExternalProcessor_ProcessClient, reqs []*extProcPb.ProcessingRequest) ([]byte, error) {
	t.Helper()
	for _, req := range reqs {
		// A server-side rejection closes the stream; the status comes from Recv.
		if err := client.Send(req); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
	}

	var body bytes.Buffer
	for {
		resp, err := client.Recv()
		if err != nil {
			return nil, err
		}
		streamed := resp.GetRequestBody().GetResponse().GetBodyMutation().GetStreamedResponse()
		if streamed == nil {
			continue
		}
		body.Write(streamed.GetBody())
		if streamed.GetEndOfStream() {
			return body.Bytes(), nil
		}
	}
}

func TestLargeRequestBody(t *testing.T) {
	prompt := strings.Repeat("a", 5<<20)
	reqBody := fmt.Sprintf(`{"model":"qwen","prompt":%q}`, prompt)

	tests := []struct {
		name               string
		grpcMaxRecvMsgSize int
		gzip               bool
		wantCode           codes.Code
	}{
		{name: "default limit rejects", wantCode: codes.ResourceExhausted},
		{name: "raised limit accepts", grpcMaxRecvMsgSize: 8 << 20, wantCode: codes.OK},
		{name: "default limit rejects gzip", gzip: true, wantCode: codes.ResourceExhausted},
		{name: "raised limit accepts gzip", grpcMaxRecvMsgSize: 8 << 20, gzip: true, wantCode: codes.OK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHarness(t, context.Background(), func(r *runserver.ExtProcServerRunner) {
				r.GRPCMaxRecvMsgSize = tt.grpcMaxRecvMsgSize
			})

			client := h.Client
			if tt.gzip {
				conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", h.port),
					grpc.WithTransportCredentials(insecure.NewCredentials()),
					grpc.WithDefaultCallOptions(grpc.UseCompressor(gzip.Name)))
				require.NoError(t, err)
				t.Cleanup(func() { _ = conn.Close() })
				client, err = extProcPb.NewExternalProcessorClient(conn).Process(t.Context())
				require.NoError(t, err)
			}

			reqs := integration.ReqRaw(map[string]string{"content-type": "application/json"}, reqBody)
			got, err := sendAndCollectBody(t, client, reqs)
			require.Equal(t, tt.wantCode, status.Code(err), "error: %v", err)
			if tt.wantCode != codes.OK {
				return
			}

			var parsed map[string]any
			require.NoError(t, json.Unmarshal(got, &parsed))
			require.Equal(t, prompt, parsed["prompt"])
		})
	}
}
