/*
Copyright 2025 The Kubernetes Authors.

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
	"context"
	"testing"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/plugins/basemodelextractor"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/plugins/bodyfieldtoheader"
	runserver "github.com/llm-d/llm-d-inference-payload-processor/pkg/server"
	"sigs.k8s.io/gateway-api-inference-extension/test/integration"
)

const modelField = "model"

var logger = logutil.NewTestLogger().V(logutil.VERBOSE)

// Harness encapsulates the environment for a single isolated payload processor test run.
type Harness struct {
	t      *testing.T
	Client extProcPb.ExternalProcessor_ProcessClient

	// Internal handles for cleanup
	server   *runserver.ExtProcServerRunner
	grpcConn *grpc.ClientConn
}

// NewHarness boots up an isolated payload processor server on a random port with the default
// BodyFieldToHeaderPlugin for model extraction and no response plugins.
func NewHarness(t *testing.T, ctx context.Context, streaming bool) *Harness {
	t.Helper()
	modelToHeaderPlugin, err := bodyfieldtoheader.NewBodyFieldToHeaderPlugin(modelField, bodyfieldtoheader.ModelHeader)
	require.NoError(t, err, "failed to create body-field-to-header plugin")

	testConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model-mappings",
			Namespace: "default",
			Labels: map[string]string{
				"inference.llm-d.io/ipp-managed": "true",
			},
		},
		Data: map[string]string{
			"baseModel": "qwen",
			"adapters": `
- sql-lora-sheddable
- foo
- 1
`,
		},
	}

	store := basemodelextractor.NewAdaptersStore()
	fakeClient := fake.NewClientBuilder().WithObjects(testConfigMap).Build()
	reconciler := &basemodelextractor.ConfigMapReconciler{
		Reader:        fakeClient,
		AdaptersStore: store,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: testConfigMap.Namespace,
			Name:      testConfigMap.Name,
		},
	}
	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err, "failed to reconcile configmap with test data")

	baseModelToHeaderPlugin := &basemodelextractor.BaseModelToHeaderPlugin{AdaptersStore: store}

	return NewHarnessWithPlugins(t, ctx, streaming, []framework.RequestProcessor{modelToHeaderPlugin, baseModelToHeaderPlugin}, []framework.ResponseProcessor{})
}

// NewHarnessWithPlugins boots up an isolated payload processor server on a random port
// with the given request and response plugins.
func NewHarnessWithPlugins(
	t *testing.T,
	ctx context.Context,
	streaming bool,
	requestPlugins []framework.RequestProcessor,
	responsePlugins []framework.ResponseProcessor,
) *Harness {
	t.Helper()

	// 1. Allocate Free Port
	port, err := integration.GetFreePort()
	require.NoError(t, err, "failed to acquire free port for payload processor server")

	// 2. Configure payload processor server with plugins
	runner := runserver.NewDefaultExtProcServerRunner(port, false)
	runner.SecureServing = false
	runner.Streaming = streaming
	runner.RequestPlugins = requestPlugins
	runner.ResponsePlugins = responsePlugins

	// 3. Start Server in Background
	serverCtx, serverCancel := context.WithCancel(ctx)

	runnable := runner.AsRunnable(logger.WithName("payload-processor-server")).Start
	client, conn := integration.StartExtProcServer(
		t,
		serverCtx,
		runnable,
		port,
		logger,
	)

	h := &Harness{
		t:        t,
		Client:   client,
		server:   runner,
		grpcConn: conn,
	}

	// 4. Register Cleanup
	t.Cleanup(func() {
		logger.Info("Tearing down payload processor server", "port", port)
		serverCancel()
		if err := h.grpcConn.Close(); err != nil {
			t.Logf("Warning: failed to close grpc connection: %v", err)
		}
	})

	return h
}
