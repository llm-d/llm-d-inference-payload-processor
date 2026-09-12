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
	"strconv"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/version"
)

type errorHandler struct {
	logger logr.Logger
}

func (h *errorHandler) Handle(err error) {
	h.logger.V(logging.DEFAULT).Error(err, "trace error occurred")
}

func InitTracing(ctx context.Context, logger logr.Logger, defaultServiceName string) error {
	logger = logger.WithName("trace")
	loggerWrap := &errorHandler{logger: logger}

	_, ok := os.LookupEnv("OTEL_SERVICE_NAME")
	if !ok {
		os.Setenv("OTEL_SERVICE_NAME", defaultServiceName)
	}

	exporterType, err := traceExporterType()
	if err != nil {
		loggerWrap.Handle(fmt.Errorf("trace exporter configuration degraded: %w", err))
	}

	logger.Info("init OTel trace exporter", "type", exporterType)

	// Go SDK doesn't have an automatic sampler, handle manually
	samplerType, ok := os.LookupEnv("OTEL_TRACES_SAMPLER")
	if !ok {
		samplerType = "parentbased_traceidratio"
	}
	samplerARG, ok := os.LookupEnv("OTEL_TRACES_SAMPLER_ARG")
	if !ok {
		samplerARG = "0.1"
	}

	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(0.1))
	if samplerType == "parentbased_traceidratio" {
		fraction, err := strconv.ParseFloat(samplerARG, 64)
		if err != nil {
			fraction = 0.1
		}

		sampler = sdktrace.ParentBased(sdktrace.TraceIDRatioBased(fraction))
	} else {
		loggerWrap.Handle(fmt.Errorf("unsupported sampler type: %s, fallback to parentbased_traceidratio with 0.1 Ratio", samplerType))
	}

	opt := []sdktrace.TracerProviderOption{
		sdktrace.WithSampler(sampler),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceVersionKey.String(version.BuildRef),
		)),
	}

	// "none" registers no span processor at all. Spans are still created and
	// propagated, so instrumented code and context propagation are unaffected.
	if exporterType != exporterTypeNone {
		traceExporter, err := newTraceExporter(ctx, exporterType)
		if err != nil {
			loggerWrap.Handle(fmt.Errorf("%s: %v", "init trace exporter failed", err))
			return err
		}
		opt = append(opt, sdktrace.WithBatcher(traceExporter))
	}

	tracerProvider := sdktrace.NewTracerProvider(opt...)
	otel.SetTracerProvider(tracerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	otel.SetErrorHandler(loggerWrap)

	go func() {
		<-ctx.Done()
		err := tracerProvider.Shutdown(context.Background())
		if err != nil {
			loggerWrap.Handle(fmt.Errorf("%s: %v", "failed to shutdown TraceProvider", err))
		}

		logger.V(logging.DEFAULT).Info("trace provider shutting down")
	}()

	return nil
}

// The exporter types OTEL_TRACES_EXPORTER selects between.
const (
	exporterTypeOTLP    = "otlp"
	exporterTypeConsole = "console"
	exporterTypeNone    = "none"

	defaultExporterType = exporterTypeOTLP
)

// traceExporterType resolves OTEL_TRACES_EXPORTER to one of the types
// newTraceExporter builds:
//
//   - otlp: export spans through gRPC to an opentelemetry collector
//   - console: pretty print spans on stdout, for development
//   - none: create spans but export nothing
//
// An unrecognised value is returned as an error alongside the default type, so a
// typo is reported rather than quietly selecting an exporter the operator did not
// ask for. The exporter is not worth failing startup over.
func traceExporterType() (string, error) {
	exporterType, ok := os.LookupEnv("OTEL_TRACES_EXPORTER")
	if !ok {
		return defaultExporterType, nil
	}

	switch exporterType {
	case exporterTypeOTLP, exporterTypeConsole, exporterTypeNone:
		return exporterType, nil
	default:
		return defaultExporterType, fmt.Errorf("unsupported OTEL_TRACES_EXPORTER %q, falling back to %s", exporterType, defaultExporterType)
	}
}

// newTraceExporter builds the exporter named by exporterType, which traceExporterType
// has already narrowed. Exactly one exporter is constructed; exporterTypeNone builds
// none and is handled by the caller.
func newTraceExporter(ctx context.Context, exporterType string) (sdktrace.SpanExporter, error) {
	if exporterType == exporterTypeConsole {
		traceExporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			return nil, fmt.Errorf("failed to create stdouttrace exporter: %w", err)
		}
		return traceExporter, nil
	}

	if _, ok := os.LookupEnv("OTEL_EXPORTER_OTLP_ENDPOINT"); !ok {
		os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317")
	}

	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("failed to create otlp-grpc exporter: %w", err)
	}
	return traceExporter, nil
}
