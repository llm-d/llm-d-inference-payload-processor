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
	"testing"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/test/utils"
)

// uppercaseChunkProcessor is a test ChunkProcessor that uppercases all bytes.
type uppercaseChunkProcessor struct{}

func (p *uppercaseChunkProcessor) ProcessResponseChunk(_ context.Context, _ *plugin.CycleState, chunk []byte, _ bool) ([]byte, error) {
	return bytes.ToUpper(chunk), nil
}

var _ requesthandling.ChunkProcessor = &uppercaseChunkProcessor{}

func sendRequestPhase(t *testing.T, process extProcPb.ExternalProcessor_ProcessClient) {
	t.Helper()
	if err := process.Send(&extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_RequestHeaders{},
	}); err != nil {
		t.Fatalf("send request headers: %v", err)
	}
	if err := process.Send(&extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_RequestBody{
			RequestBody: &extProcPb.HttpBody{
				Body:        []byte(`{"model":"test"}`),
				EndOfStream: true,
			},
		},
	}); err != nil {
		t.Fatalf("send request body: %v", err)
	}
	if msg, err := process.Recv(); err != nil || msg.GetRequestHeaders() == nil {
		t.Fatalf("expected RequestHeaders response, err=%v", err)
	}
	if msg, err := process.Recv(); err != nil || msg.GetRequestBody() == nil {
		t.Fatalf("expected RequestBody response, err=%v", err)
	}
}

func sendResponseHeaders(t *testing.T, process extProcPb.ExternalProcessor_ProcessClient) {
	t.Helper()
	respHeaders := utils.BuildEnvoyGRPCHeaders(map[string]string{
		"content-type": "text/event-stream",
	}, true)
	if err := process.Send(&extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_ResponseHeaders{
			ResponseHeaders: respHeaders,
		},
	}); err != nil {
		t.Fatalf("send response headers: %v", err)
	}
}

func wantChunkAck(body []byte, eos bool) *extProcPb.ProcessingResponse {
	return &extProcPb.ProcessingResponse{
		Response: &extProcPb.ProcessingResponse_ResponseBody{
			ResponseBody: &extProcPb.BodyResponse{
				Response: &extProcPb.CommonResponse{
					BodyMutation: &extProcPb.BodyMutation{
						Mutation: &extProcPb.BodyMutation_StreamedResponse{
							StreamedResponse: &extProcPb.StreamedBodyResponse{
								Body:        body,
								EndOfStream: eos,
							},
						},
					},
				},
			},
		},
	}
}

func TestStreamingPassthrough_NoBuffering(t *testing.T) {
	chunks := []struct {
		body []byte
		eos  bool
	}{
		{body: []byte(`data: {"choices":[{"delta":{"content":"Hello"}}]}`), eos: false},
		{body: []byte(`data: {"choices":[{"delta":{"content":" world"}}]}`), eos: false},
		{body: []byte(`data: [DONE]`), eos: true},
	}

	streamCtx, cancel := context.WithCancel(logutil.NewTestLoggerIntoContext(context.Background()))
	profiles := map[string]*requesthandling.Profile{
		testProfileName: {
			RequestPlugins:         []requesthandling.RequestProcessor{},
			ResponsePlugins:        []requesthandling.ResponseProcessor{},
			NeedsResponseBuffering: false,
		},
	}
	srv := newServerForTest(profiles)
	testListener, errChan := utils.SetupTestStreamingServer(t, streamCtx, srv)
	process, conn := utils.GetStreamingServerClient(streamCtx, t)
	defer conn.Close()
	defer func() {
		cancel()
		<-errChan
		testListener.Close()
	}()

	sendRequestPhase(t, process)
	sendResponseHeaders(t, process)

	for _, c := range chunks {
		if err := process.Send(&extProcPb.ProcessingRequest{
			Request: &extProcPb.ProcessingRequest_ResponseBody{
				ResponseBody: &extProcPb.HttpBody{
					Body:        c.body,
					EndOfStream: c.eos,
				},
			},
		}); err != nil {
			t.Fatalf("send response body chunk: %v", err)
		}

		msg, err := process.Recv()
		if err != nil {
			t.Fatalf("recv chunk ack: %v", err)
		}

		want := wantChunkAck(c.body, c.eos)
		if diff := cmp.Diff(want, msg, protocmp.Transform()); diff != "" {
			t.Errorf("chunk ack mismatch, diff(-want, +got): %s", diff)
		}
	}
}

func TestStreamingPassthrough_WithChunkProcessor(t *testing.T) {
	chunks := []struct {
		body     []byte
		eos      bool
		wantBody []byte
	}{
		{body: []byte("hello"), eos: false, wantBody: []byte("HELLO")},
		{body: []byte("world"), eos: true, wantBody: []byte("WORLD")},
	}

	streamCtx, cancel := context.WithCancel(logutil.NewTestLoggerIntoContext(context.Background()))
	profiles := map[string]*requesthandling.Profile{
		testProfileName: {
			RequestPlugins:         []requesthandling.RequestProcessor{},
			ResponsePlugins:        []requesthandling.ResponseProcessor{},
			NeedsResponseBuffering: false,
			ChunkProcessors:        []requesthandling.ChunkProcessor{&uppercaseChunkProcessor{}},
		},
	}
	srv := newServerForTest(profiles)
	testListener, errChan := utils.SetupTestStreamingServer(t, streamCtx, srv)
	process, conn := utils.GetStreamingServerClient(streamCtx, t)
	defer conn.Close()
	defer func() {
		cancel()
		<-errChan
		testListener.Close()
	}()

	sendRequestPhase(t, process)
	sendResponseHeaders(t, process)

	for _, c := range chunks {
		if err := process.Send(&extProcPb.ProcessingRequest{
			Request: &extProcPb.ProcessingRequest_ResponseBody{
				ResponseBody: &extProcPb.HttpBody{
					Body:        c.body,
					EndOfStream: c.eos,
				},
			},
		}); err != nil {
			t.Fatalf("send response body chunk: %v", err)
		}

		msg, err := process.Recv()
		if err != nil {
			t.Fatalf("recv chunk ack: %v", err)
		}

		want := wantChunkAck(c.wantBody, c.eos)
		if diff := cmp.Diff(want, msg, protocmp.Transform()); diff != "" {
			t.Errorf("chunk ack mismatch after ChunkProcessor, diff(-want, +got): %s", diff)
		}
	}
}
