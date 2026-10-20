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

package basemodelextractor

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

func TestConfigMapReconcilerDelete(t *testing.T) {
	ctx := context.Background()
	first := makeConfigMap("first", baseModel, "- a1\n")
	second := makeConfigMap("first", baseModel, "- a2\n")
	first.Labels = map[string]string{ippManagedLabel: "true"}
	second.Namespace = "other-namespace"
	second.Labels = map[string]string{ippManagedLabel: "true"}
	k8sClient := fake.NewClientBuilder().WithObjects(first, second).Build()
	store := NewAdaptersStore()
	reconciler := &ConfigMapReconciler{Reader: k8sClient, AdaptersStore: store}
	plugin := &BaseModelToHeaderPlugin{AdaptersStore: store}
	reconcile := func(object metav1.Object) {
		t.Helper()
		if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: object.GetNamespace(), Name: object.GetName()}}); err != nil {
			t.Fatal(err)
		}
	}
	checkHeader := func(model, want string) {
		t.Helper()
		request := requesthandling.NewInferenceRequest()
		request.Body["model"] = model
		if err := plugin.ProcessRequest(ctx, nil, request); err != nil {
			t.Fatal(err)
		}
		if got := request.MutatedHeaders()[BaseModelHeader]; got != want {
			t.Errorf("base-model header for %q = %q, want %q", model, got, want)
		}
	}
	reconcile(first)
	reconcile(second)
	checkHeader("a1", baseModel)
	checkHeader("a2", baseModel)
	if err := k8sClient.Delete(ctx, first); err != nil {
		t.Fatal(err)
	}
	reconcile(first)
	reconcile(first)
	checkHeader("a1", "")
	checkHeader("a2", baseModel)
	checkHeader(baseModel, baseModel)
	if err := k8sClient.Delete(ctx, second); err != nil {
		t.Fatal(err)
	}
	reconcile(second)
	checkHeader("a2", "")
	checkHeader(baseModel, "")
}
