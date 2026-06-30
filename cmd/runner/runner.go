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

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	healthPb "google.golang.org/grpc/health/grpc_health_v1"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/llm-d/llm-d-inference-payload-processor/internal/runnable"
	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/profiling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/tracing"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/config/loader"
	ippdatalayer "github.com/llm-d/llm-d-inference-payload-processor/pkg/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/datastore/inmemory"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	modelconfigcollector "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/datalayer/modelconfigcollector"
	requestmetadata "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/datalayer/requestmetadata"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/picker/maxscore"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/picker/random"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/picker/weightedrandom"
	inflightrequestsscorer "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/scorer/inflightrequests"
	sessionaffinity "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/scorer/sessionaffinity"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/basemodelextractor"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/bodyfieldtoheader"
	modelselectorplugin "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/profilepicker/single"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/metrics"
	runserver "github.com/llm-d/llm-d-inference-payload-processor/pkg/server"
	"github.com/llm-d/llm-d-inference-payload-processor/version"
)

var setupLog = ctrl.Log.WithName("setup")

func NewRunner() *Runner {
	return &Runner{
		payloadProcessorExecutableName: "payload-processor",
		profiles:                       map[string]*requesthandling.Profile{},
		customCollectors:               []prometheus.Collector{},
		customControllers:              []func(client.Client, *ctrlbuilder.Builder) error{},
	}
}

// Runner is used to run payload processor with its plugins
type Runner struct {
	payloadProcessorExecutableName string
	// preProcessors is an array of pre-processing plugins that operate on the request
	preProcessors []requesthandling.RequestProcessor
	// profilePicker is the profile picker instantiated as specified in the configuration
	profilePicker requesthandling.ProfilePicker
	// profiles is the set of named profiles loaded from the configuration
	profiles map[string]*requesthandling.Profile
	// postProcessors is an array of post-processing plugins that operate on the response
	postProcessors []requesthandling.ResponseProcessor

	customCollectors  []prometheus.Collector
	customControllers []func(client.Client, *ctrlbuilder.Builder) error
	processor         datasource.DatalayerProcessor
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

// WithCustomControllers registers custom controllers within the
// controller manager. Use this for control-plane controllers that create
// infrastructure resources (Services, HTTPRoutes, etc.) and should not be
// coupled to data-plane plugin lifecycle.
func (r *Runner) WithCustomControllers(setupFuncs ...func(client.Client, *ctrlbuilder.Builder) error) *Runner {
	r.customControllers = setupFuncs
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
		err := tracing.InitTracing(ctx, setupLog, "llm-d-ipp")
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
	metrics.RecordIPPInfo(version.CommitSHA, version.BuildRef)
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

	for _, customControllerFunc := range r.customControllers {
		if err := customControllerFunc(mgr.GetClient(), ctrl.NewControllerManagedBy(mgr)); err != nil {
			setupLog.Error(err, "Failed to register custom controller")
			return err
		}
	}

	ds := inmemory.NewDatastore()

	err = r.loadConfiguration(ctx, opts, mgr, ds, setupLog)
	if err != nil {
		return err
	}

	if err := r.processor.Start(ctx); err != nil {
		setupLog.Error(err, "failed to start datalayer processor")
		return err
	}
	defer r.processor.Stop()

	// Setup ExtProc Server Runner.
	serverRunner := &runserver.ExtProcServerRunner{
		GrpcPort:       opts.GRPCPort,
		SecureServing:  opts.SecureServing,
		PreProcessors:  r.preProcessors,
		ProfilePicker:  r.profilePicker,
		Profiles:       r.profiles,
		PostProcessors: r.postProcessors,
		EventNotifier:  r.processor,
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

func (r *Runner) loadConfiguration(ctx context.Context, opts *runserver.Options, mgr manager.Manager, ds datalayer.Datastore, logger logr.Logger) error {
	r.processor = ippdatalayer.NewProcessor()
	handle := plugin.NewHandle(ctx, mgr, ds, r.processor)

	var configBytes []byte
	if opts.ConfigText != "" {
		configBytes = []byte(opts.ConfigText)
	} else if opts.ConfigFile != "" { // if config was specified through a file
		var err error
		configBytes, err = os.ReadFile(opts.ConfigFile)
		if err != nil {
			logger.Error(err, "failed to load config from a file", "file", opts.ConfigFile)
			return fmt.Errorf("failed to load config from a file '%s' - %w", opts.ConfigFile, err)
		}
	}

	// Register factories for all known in-tree plugins
	r.registerInTreePlugins()

	theConfig, err := loader.LoadConfiguration(configBytes, handle, r.processor, logger)
	if err != nil {
		return err
	}

	r.preProcessors = theConfig.PreProcessors
	r.profilePicker = theConfig.ProfilePicker
	r.profiles = theConfig.Profiles
	r.postProcessors = theConfig.PostProcessors

	return nil
}

// registerInTreePlugins registers the factory functions of all known payload processor plugins
func (r *Runner) registerInTreePlugins() {
	plugin.Register(single.SingleProfilePickerType, single.SingleProfilePickerFactory)
	plugin.Register(bodyfieldtoheader.BodyFieldToHeaderPluginType, bodyfieldtoheader.BodyFieldToHeaderPluginFactory)
	plugin.Register(basemodelextractor.BaseModelToHeaderPluginType, basemodelextractor.BaseModelToHeaderPluginFactory)
	plugin.Register(requestmetadata.PluginType, requestmetadata.ExtractorFactory)
	plugin.Register(modelconfigcollector.PluginType, modelconfigcollector.DatasourceFactory)
	// register model selector plugins
	plugin.Register(random.RandomPickerType, random.RandomPickerFactory)
	plugin.Register(maxscore.MaxScorePickerType, maxscore.MaxScorePickerFactory)
	plugin.Register(weightedrandom.WeightedRandomPickerType, weightedrandom.WeightedRandomPickerFactory)
	plugin.Register(modelselectorplugin.ModelSelectorPluginType, modelselectorplugin.ModelSelectorPluginFactory)
	plugin.Register(inflightrequestsscorer.PluginType, inflightrequestsscorer.ScorerFactory)
	plugin.Register(sessionaffinity.PluginType, sessionaffinity.ScorerFactory)
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
