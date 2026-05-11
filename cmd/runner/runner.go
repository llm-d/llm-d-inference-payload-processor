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

package runner

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	healthPb "google.golang.org/grpc/health/grpc_health_v1"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/llm-d/llm-d-inference-payload-processor/internal/runnable"
	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/profiling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/tracing"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/metrics"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/plugins/basemodelextractor"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/plugins/bodyfieldtoheader"
	runserver "github.com/llm-d/llm-d-inference-payload-processor/pkg/server"
	"github.com/llm-d/llm-d-inference-payload-processor/version"
)

const modelField = "model"

var setupLog = ctrl.Log.WithName("setup")

func NewRunner() *Runner {
	return &Runner{
		payloadProcessorExecutableName: "payload-processor",
		requestPlugins:                 []framework.RequestProcessor{},
		responsePlugins:                []framework.ResponseProcessor{},
		customCollectors:               []prometheus.Collector{},
	}
}

// Runner is used to run payload processor with its plugins
type Runner struct {
	payloadProcessorExecutableName string
	// request processing plugin instances executed by the request handler,
	// in the same order the plugin flags are provided.
	requestPlugins []framework.RequestProcessor
	// response processing plugin instances executed by the response handler,
	// in the same order the plugin flags are provided.
	responsePlugins []framework.ResponseProcessor

	customCollectors []prometheus.Collector
}

// WithExecutableName sets the name of the executable containing the runner.
// The name is used in the version log upon startup and is otherwise opaque.
func (r *Runner) WithExecutableName(exeName string) *Runner {
	r.payloadProcessorExecutableName = exeName
	return r
}

// WithCustomCollectors sets custom prometheus metrics collectors
func (r *Runner) WithCustomCollectors(collectors ...prometheus.Collector) *Runner {
	r.customCollectors = collectors
	return r
}

func (r *Runner) Run(ctx context.Context) error {
	// Setup a basic logger in case command-line argument parsing fails.
	logutil.InitSetupLogging()

	setupLog.Info(r.payloadProcessorExecutableName+" build", "commit-sha", version.CommitSHA, "build-ref", version.BuildRef)

	opts := runserver.NewOptions()
	opts.AddFlags(pflag.CommandLine)
	pflag.Parse()

	if err := opts.Complete(); err != nil {
		return err
	}
	if err := opts.Validate(); err != nil {
		setupLog.Error(err, "Failed to validate flags")
		return err
	}

	// Print all flag values.
	flags := make(map[string]any)
	pflag.VisitAll(func(f *pflag.Flag) {
		flags[f.Name] = f.Value
	})

	if opts.Tracing {
		err := tracing.InitTracing(ctx, setupLog, "llm-d/inference-payload-processor")
		if err != nil {
			setupLog.Error(err, "failed to initialize tracing")
			return err
		}
	}

	setupLog.Info("Flags processed", "flags", flags)

	logutil.InitLogging(&opts.ZapOptions)

	// Init runtime.
	cfg, err := ctrl.GetConfig()
	if err != nil {
		setupLog.Error(err, "Failed to get rest config")
		return err
	}

	// --- Setup Metrics Server ---
	metrics.Register(r.customCollectors...)
	metrics.RecordBBRInfo(version.CommitSHA, version.BuildRef)
	// Register metrics handler.
	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress: fmt.Sprintf(":%d", opts.MetricsPort),
		FilterProvider: func() func(c *rest.Config, httpClient *http.Client) (metricsserver.Filter, error) {
			if opts.MetricsEndpointAuth {
				return filters.WithAuthenticationAndAuthorization
			}

			return nil
		}(),
	}
	cacheOptions := cache.Options{}
	// Apply namespace filtering only if env var is set.
	namespace := os.Getenv("NAMESPACE")
	if namespace != "" {
		cacheOptions.DefaultNamespaces = map[string]cache.Config{
			namespace: {},
		}
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{Cache: cacheOptions, Metrics: metricsServerOptions})
	if err != nil {
		setupLog.Error(err, "Failed to create manager", "config", cfg)
		return err
	}

	if opts.EnablePprof {
		setupLog.Info("Setting pprof handlers")
		if err = profiling.SetupPprofHandlers(mgr); err != nil {
			setupLog.Error(err, "Failed to setup pprof handlers")
			return err
		}
	}

	handle := framework.NewHandle(ctx, mgr)

	// Register factories for all known in-tree plugins
	r.registerInTreePlugins()

	// Construct plugin instances for the in-tree plugins that are (1) registered and (2) requested via the --plugin flags
	if len(opts.PluginSpecs) == 0 {
		setupLog.Info("no plugins are specified. Running with the default plugins")

		modelToHeaderPlugin, err := bodyfieldtoheader.NewBodyFieldToHeaderPlugin(modelField, bodyfieldtoheader.ModelHeader)
		if err != nil {
			setupLog.Error(err, "failed to create plugin", "pluginType", bodyfieldtoheader.BodyFieldToHeaderPluginType)
			return err
		}
		r.requestPlugins = append(r.requestPlugins, modelToHeaderPlugin)

		// Create BaseModelToHeaderPlugin instance for extracting the "model" field into X-Gateway-Base-Model-Name
		baseModelToHeaderPlugin, err := basemodelextractor.NewBaseModelToHeaderPlugin(func() *builder.Builder {
			return ctrl.NewControllerManagedBy(mgr)
		}, mgr.GetClient())
		if err != nil {
			setupLog.Error(err, "failed to create plugin", "pluginType", basemodelextractor.BaseModelToHeaderPluginType)
			return err
		}

		r.requestPlugins = append(r.requestPlugins, baseModelToHeaderPlugin)
	} else {
		setupLog.Info("plugins are specified, running with the specified plugins.")

		for _, s := range opts.PluginSpecs {
			factory, ok := framework.Registry[s.Type]
			if !ok {
				err := fmt.Errorf("unknown plugin type %q (no factory registered)", s.Type)
				setupLog.Error(err, "Failed to find plugin factory", "pluginType", s.Type)
				return err
			}
			instance, err := factory(s.Name, s.JSON, handle)
			if err != nil {
				setupLog.Error(err, fmt.Sprintf("invalid %s#%s: %v\n", s.Type, s.Name, err))
				return err
			}
			if requestProcessor, ok := instance.(framework.RequestProcessor); ok {
				r.requestPlugins = append(r.requestPlugins, requestProcessor)
			}
			if responseProcessor, ok := instance.(framework.ResponseProcessor); ok {
				r.responsePlugins = append(r.responsePlugins, responseProcessor)
			}
		}
	}

	// Setup ExtProc Server Runner.
	serverRunner := &runserver.ExtProcServerRunner{
		GrpcPort:        opts.GRPCPort,
		SecureServing:   opts.SecureServing,
		RequestPlugins:  r.requestPlugins,
		ResponsePlugins: r.responsePlugins,
	}

	// Register health server.
	if err := registerHealthServer(mgr, opts.GRPCHealthPort); err != nil {
		return err
	}

	// Register ext-proc server.
	if err := mgr.Add(serverRunner.AsRunnable(ctrl.Log.WithName("ext-proc"))); err != nil {
		setupLog.Error(err, "Failed to register ext-proc gRPC server")
		return err
	}

	// Start the manager. This blocks until a signal is received.
	setupLog.Info("manager starting")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "Error starting manager")
		return err
	}
	setupLog.Info("manager terminated")
	return nil
}

// registerInTreePlugins registers the factory functions of all known payload processor plugins
func (r *Runner) registerInTreePlugins() {
	framework.Register(bodyfieldtoheader.BodyFieldToHeaderPluginType, bodyfieldtoheader.BodyFieldToHeaderPluginFactory)
	framework.Register(basemodelextractor.BaseModelToHeaderPluginType, basemodelextractor.BaseModelToHeaderPluginFactory)
}

// registerHealthServer adds the Health gRPC server as a Runnable to the given manager.
func registerHealthServer(mgr manager.Manager, port int) error {
	srv := grpc.NewServer()
	healthPb.RegisterHealthServer(srv, &healthServer{})
	if err := mgr.Add(
		runnable.NoLeaderElection(runnable.GRPCServer("health", srv, port))); err != nil {
		setupLog.Error(err, "Failed to register health server")
		return err
	}
	return nil
}
