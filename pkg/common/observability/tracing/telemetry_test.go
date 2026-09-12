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
	"fmt"
	"os"
	"testing"
)

func clearEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := os.LookupEnv(key); ok {
			orig := os.Getenv(key)
			if err := os.Unsetenv(key); err != nil {
				t.Fatalf("Unsetenv(%q) error = %v", key, err)
			}
			t.Cleanup(func() { os.Setenv(key, orig) })
		}
	}
}

func ptr(s string) *string { return &s }

func TestTraceExporterType(t *testing.T) {
	tests := []struct {
		name    string
		env     *string
		want    string
		wantErr bool
	}{
		{name: "unset defaults to otlp", env: nil, want: "otlp"},
		{name: "otlp", env: ptr("otlp"), want: "otlp"},
		{name: "console", env: ptr("console"), want: "console"},
		{name: "none", env: ptr("none"), want: "none"},
		{name: "an unrecognised value is reported", env: ptr("jaeger"), want: "otlp", wantErr: true},
		{name: "an empty value is reported rather than treated as unset", env: ptr(""), want: "otlp", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t, "OTEL_TRACES_EXPORTER")
			if tc.env != nil {
				t.Setenv("OTEL_TRACES_EXPORTER", *tc.env)
			}

			got, err := traceExporterType()
			if (err != nil) != tc.wantErr {
				t.Fatalf("traceExporterType() error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("traceExporterType() = %q, want %q", got, tc.want)
			}
		})
	}
}

// newTraceExporter must build exactly the exporter it was asked for. The stdout
// exporter in particular must not be constructed for the otlp type.
func TestNewTraceExporter(t *testing.T) {
	clearEnv(t, "OTEL_EXPORTER_OTLP_ENDPOINT")

	tests := []struct {
		exporterType string
		wantType     string
	}{
		{exporterType: exporterTypeOTLP, wantType: "*otlptrace.Exporter"},
		{exporterType: exporterTypeConsole, wantType: "*stdouttrace.Exporter"},
	}

	for _, tc := range tests {
		t.Run(tc.exporterType, func(t *testing.T) {
			exporter, err := newTraceExporter(context.Background(), tc.exporterType)
			if err != nil {
				t.Fatalf("newTraceExporter(%q) error = %v", tc.exporterType, err)
			}
			t.Cleanup(func() { _ = exporter.Shutdown(context.Background()) })

			if got := fmt.Sprintf("%T", exporter); got != tc.wantType {
				t.Errorf("newTraceExporter(%q) = %s, want %s", tc.exporterType, got, tc.wantType)
			}
		})
	}
}

// The otlp exporter falls back to a loopback endpoint only when the operator has
// not configured one; "none" and "console" must not touch the environment at all.
func TestNewTraceExporterOTLPDefaultsEndpointOnlyWhenUnset(t *testing.T) {
	clearEnv(t, "OTEL_EXPORTER_OTLP_ENDPOINT")
	t.Cleanup(func() { os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT") })

	exporter, err := newTraceExporter(context.Background(), exporterTypeOTLP)
	if err != nil {
		t.Fatalf("newTraceExporter(otlp) error = %v", err)
	}
	t.Cleanup(func() { _ = exporter.Shutdown(context.Background()) })

	if got := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); got != "http://localhost:4317" {
		t.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT = %q, want the loopback default", got)
	}
}

func TestNewTraceExporterConsoleDoesNotSetEndpoint(t *testing.T) {
	clearEnv(t, "OTEL_EXPORTER_OTLP_ENDPOINT")

	exporter, err := newTraceExporter(context.Background(), exporterTypeConsole)
	if err != nil {
		t.Fatalf("newTraceExporter(console) error = %v", err)
	}
	t.Cleanup(func() { _ = exporter.Shutdown(context.Background()) })

	if _, ok := os.LookupEnv("OTEL_EXPORTER_OTLP_ENDPOINT"); ok {
		t.Error("OTEL_EXPORTER_OTLP_ENDPOINT was set for the console exporter, want it left untouched")
	}
}
