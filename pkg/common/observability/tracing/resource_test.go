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

package tracing

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"

	"github.com/llm-d/llm-d-inference-payload-processor/version"
)

func attrMap(t *testing.T, res interface {
	Attributes() []attribute.KeyValue
}) map[attribute.Key]attribute.Value {
	t.Helper()
	out := map[attribute.Key]attribute.Value{}
	for _, kv := range res.Attributes() {
		out[kv.Key] = kv.Value
	}
	return out
}

func TestNewResourceDefaults(t *testing.T) {
	// An empty value reads the same as unset, per the OTel environment
	// variable specification.
	t.Setenv("OTEL_SERVICE_NAME", "")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")

	res, err := newResource(context.Background(), "llm-d-ipp")
	if err != nil {
		t.Fatalf("newResource() error = %v", err)
	}

	attrs := attrMap(t, res)
	if got, want := attrs[semconv.ServiceNameKey].AsString(), "llm-d-ipp"; got != want {
		t.Errorf("service.name = %v, want %q", got, want)
	}
	if got := attrs[semconv.ServiceVersionKey].AsString(); got != version.BuildRef {
		t.Errorf("service.version = %v, want %v", got, version.BuildRef)
	}
	if res.SchemaURL() != semconv.SchemaURL {
		t.Errorf("schema URL = %q, want %q", res.SchemaURL(), semconv.SchemaURL)
	}
}

func TestNewResourceEnvOverrides(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "custom-service")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=prod,k8s.pod.name=ipp-0")

	res, err := newResource(context.Background(), "llm-d-ipp")
	if err != nil {
		t.Fatalf("newResource() error = %v", err)
	}

	attrs := attrMap(t, res)
	if got, want := attrs[semconv.ServiceNameKey].AsString(), "custom-service"; got != want {
		t.Errorf("service.name = %v, want %q (OTEL_SERVICE_NAME must win)", got, want)
	}
	if got, want := attrs[attribute.Key("deployment.environment")].AsString(), "prod"; got != want {
		t.Errorf("deployment.environment = %v, want %q", got, want)
	}
	if got, want := attrs[attribute.Key("k8s.pod.name")].AsString(), "ipp-0"; got != want {
		t.Errorf("k8s.pod.name = %v, want %q", got, want)
	}
}

func TestNewResourceMalformedAttrsDegrade(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "good.key=ok,malformed,key.without.value=bad")

	res, err := newResource(context.Background(), "llm-d-ipp")
	if err == nil {
		t.Error("newResource() error = nil, want a degradation report for malformed entries")
	}
	if res == nil {
		t.Fatal("newResource() resource = nil, want a partial resource")
	}

	attrs := attrMap(t, res)
	if got, want := attrs[semconv.ServiceNameKey].AsString(), "llm-d-ipp"; got != want {
		t.Errorf("service.name = %v, want %q", got, want)
	}
	if got, want := attrs[attribute.Key("good.key")].AsString(), "ok"; got != want {
		t.Errorf("good.key = %v, want %q", got, want)
	}
	if res.SchemaURL() != semconv.SchemaURL {
		t.Errorf("schema URL = %q, want %q", res.SchemaURL(), semconv.SchemaURL)
	}
}
