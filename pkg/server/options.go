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

package server

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
)

const (
	DefaultGrpcPort       = 9004
	DefaultGrpcHealthPort = 9005
)

// Options contains the command-line configuration for the IPP server.
type Options struct {
	//
	// ext_proc configuration.
	//
	GRPCPort              int    // gRPC port for communicating with Envoy proxy.
	GRPCMaxRecvMsgSize    int    // Maximum gRPC message size to receive in bytes; 0 keeps the gRPC default.
	GRPCMaxRecvMsgSizeStr string // Raw grpc-max-recv-msg-size flag value, parsed by Complete.
	//
	// Diagnostics.
	//
	logging.LoggingOptions      // Logging configuration.
	Tracing                bool // Enable emitting traces
	MetricsPort            int  // The metrics port exposed by IPP.
	GRPCHealthPort         int  // The port for gRPC liveness and readiness probes.
	EnablePprof            bool // Enables pprof handlers.
	SecureServing          bool // Enables secure serving.
	MetricsEndpointAuth    bool // Enables authentication and authorization of the metrics endpoint.
	//
	// Configuration.
	//
	ConfigFile string // The path to the configuration file.
	ConfigText string // The configuration specified as text, in lieu of a file.

	// internal
	fs *pflag.FlagSet // FlagSet used in AddFlags()
}

// NewOptions returns a new Options struct initialized with default values.
func NewOptions() *Options {
	return &Options{
		GRPCPort:            DefaultGrpcPort,
		GRPCHealthPort:      DefaultGrpcHealthPort,
		LoggingOptions:      *logging.NewOptions(),
		Tracing:             true,
		MetricsPort:         9090,
		EnablePprof:         true,
		SecureServing:       true,
		MetricsEndpointAuth: true,
	}
}

// AddFlags binds the Options fields to command-line flags on the given FlagSet.
func (opts *Options) AddFlags(fs *pflag.FlagSet) {
	if fs == nil {
		fs = pflag.CommandLine
	}

	opts.fs = fs

	fs.IntVar(&opts.GRPCPort, "grpc-port", opts.GRPCPort,
		"The gRPC port used for communicating with Envoy proxy.")
	fs.StringVar(&opts.GRPCMaxRecvMsgSizeStr, "grpc-max-recv-msg-size", opts.GRPCMaxRecvMsgSizeStr,
		"Maximum size of a gRPC message received from Envoy (e.g., 32MiB, 25MB). Defaults to the gRPC limit of 4MiB.")
	fs.IntVar(&opts.GRPCHealthPort, "grpc-health-port", opts.GRPCHealthPort,
		"The port used for gRPC liveness and readiness probes.")
	fs.IntVar(&opts.MetricsPort, "metrics-port", opts.MetricsPort,
		"The metrics port exposed by IPP.")
	fs.BoolVar(&opts.Tracing, "tracing", opts.Tracing, "Enables emitting traces.")
	fs.BoolVar(&opts.MetricsEndpointAuth, "metrics-endpoint-auth", opts.MetricsEndpointAuth,
		"Enables authentication and authorization of the metrics endpoint.")
	fs.BoolVar(&opts.SecureServing, "secure-serving", opts.SecureServing,
		"Enables secure serving.")
	fs.BoolVar(&opts.EnablePprof, "enable-pprof", opts.EnablePprof,
		"Enables pprof handlers. Defaults to true. Set to false to disable pprof handlers.")

	fs.StringVar(&opts.ConfigFile, "config-file", opts.ConfigFile, "The path to the configuration file.")
	fs.StringVar(&opts.ConfigText, "config-text", opts.ConfigText, "The configuration specified as text, in lieu of a file.")

	opts.LoggingOptions.AddFlags(fs) // Add logging flags.
}

// Complete performs post-processing of parsed command-line arguments.
func (opts *Options) Complete() error {
	if opts.GRPCMaxRecvMsgSizeStr != "" {
		size, err := parseByteSize(opts.GRPCMaxRecvMsgSizeStr)
		if err != nil {
			return fmt.Errorf("invalid value %q for flag %q: %w", opts.GRPCMaxRecvMsgSizeStr, "grpc-max-recv-msg-size", err)
		}
		opts.GRPCMaxRecvMsgSize = size
	}

	// Complete logging options.
	return opts.LoggingOptions.Complete()
}

// parseByteSize parses a quantity such as 32Mi or 25M into bytes, also accepting
// a trailing B (32MiB, 25MB) to match the router's size flags.
func parseByteSize(value string) (int, error) {
	s := strings.TrimSpace(value)
	if len(s) > 1 && (s[len(s)-1] == 'B' || s[len(s)-1] == 'b') {
		s = s[:len(s)-1]
	}
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return 0, err
	}
	n, ok := q.AsInt64()
	if !ok || n < 0 {
		return 0, errors.New("must be a non-negative whole number of bytes")
	}
	return int(n), nil
}

// Validate checks the Options for invalid or conflicting values.
func (opts *Options) Validate() error {
	// Validate port ranges.
	for _, pc := range []struct {
		name string
		port int
	}{
		{"grpc-port", opts.GRPCPort},
		{"grpc-health-port", opts.GRPCHealthPort},
		{"metrics-port", opts.MetricsPort},
	} {
		if pc.port < 1 || pc.port > 65535 {
			return fmt.Errorf("invalid value %d for flag %q: must be between 1 and 65535", pc.port, pc.name)
		}
	}

	// Validate that the three server ports do not collide.
	ports := map[int]string{
		opts.GRPCPort:       "grpc-port",
		opts.GRPCHealthPort: "grpc-health-port",
		opts.MetricsPort:    "metrics-port",
	}
	if len(ports) < 3 {
		return fmt.Errorf("port conflict: grpc-port (%d), grpc-health-port (%d), and metrics-port (%d) must all be different",
			opts.GRPCPort, opts.GRPCHealthPort, opts.MetricsPort)
	}

	// Validate logging options.
	if err := opts.LoggingOptions.Validate(); err != nil {
		return err
	}

	return nil
}
