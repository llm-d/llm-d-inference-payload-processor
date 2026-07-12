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

package llmdroutermetricscollector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	dlsrc "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	ippmetrics "github.com/llm-d/llm-d-inference-payload-processor/pkg/metrics"
)

// Failure reasons for the kv_cache_scrape_failures_total label (closed set).
const (
	failReasonRequestBuild = "request_build"
	failReasonTimeout      = "timeout"
	failReasonDial         = "dial"
	failReasonHTTPStatus   = "http_status"
	failReasonParse        = "parse"
)

const (
	PluginType = "router-metrics-collector"

	defaultInterval = 30 * time.Second
	defaultTimeout  = 5 * time.Second
	// Older vLLM uses "vllm:gpu_cache_usage_perc"; override via utilizationMetric.
	defaultUtilizationMetric     = "vllm:kv_cache_usage_perc"
	defaultQueueDepthMetric      = "vllm:num_requests_waiting"
	defaultCPUCacheUsageMetric   = "vllm:cpu_cache_usage_perc"
	defaultRunningRequestsMetric = "vllm:num_requests_running"
)

var _ dlsrc.Collector = &RouterMetricsCollector{}

// CollectorConfig is the optional JSON config; empty fields use defaults.
type CollectorConfig struct {
	Interval              string `json:"interval,omitempty"`
	Timeout               string `json:"timeout,omitempty"`
	MaxConcurrent         int    `json:"maxConcurrent,omitempty"`
	UtilizationMetric     string `json:"utilizationMetric,omitempty"`
	QueueDepthMetric      string `json:"queueDepthMetric,omitempty"`
	CPUCacheUsageMetric   string `json:"cpuCacheUsageMetric,omitempty"`
	RunningRequestsMetric string `json:"runningRequestsMetric,omitempty"`
}

// RouterMetricsCollector polls model metricsURLs.
// When maxConcurrent > 1, targets are scraped in parallel using a semaphore.
type RouterMetricsCollector struct {
	typedName  plugin.TypedName
	ds         datalayer.Datastore
	httpClient *http.Client

	interval              time.Duration
	timeout               time.Duration
	maxConcurrent         int
	utilizationMetric     string
	queueDepthMetric      string
	cpuCacheUsageMetric   string
	runningRequestsMetric string
}

// NewRouterMetricsCollector returns a collector with defaults.
func NewRouterMetricsCollector(ds datalayer.Datastore) *RouterMetricsCollector {
	return &RouterMetricsCollector{
		typedName:             plugin.TypedName{Type: PluginType, Name: PluginType},
		ds:                    ds,
		httpClient:            &http.Client{}, // no Client.Timeout; per-scrape timeout via context
		interval:              defaultInterval,
		timeout:               defaultTimeout,
		utilizationMetric:     defaultUtilizationMetric,
		queueDepthMetric:      defaultQueueDepthMetric,
		cpuCacheUsageMetric:   defaultCPUCacheUsageMetric,
		runningRequestsMetric: defaultRunningRequestsMetric,
	}
}

// CollectorFactory parses and validates JSON config into a RouterMetricsCollector.
func CollectorFactory(name string, raw json.RawMessage, h plugin.Handle) (plugin.Plugin, error) {
	cfg := CollectorConfig{Interval: defaultInterval.String(), Timeout: defaultTimeout.String()}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("plugin %q: parse parameters: %w", name, err)
		}
	}

	interval, err := parsePositive(cfg.Interval)
	if err != nil {
		return nil, fmt.Errorf("plugin %q: invalid interval: %w", name, err)
	}
	timeout, err := parsePositive(cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("plugin %q: invalid timeout: %w", name, err)
	}
	if cfg.MaxConcurrent < 0 {
		return nil, fmt.Errorf("plugin %q: maxConcurrent must be >= 0", name)
	}

	c := NewRouterMetricsCollector(h.Datastore())
	c.typedName.Name = name
	c.interval = interval
	c.timeout = timeout
	c.maxConcurrent = cfg.MaxConcurrent
	c.utilizationMetric = orDefault(cfg.UtilizationMetric, defaultUtilizationMetric)
	c.queueDepthMetric = orDefault(cfg.QueueDepthMetric, defaultQueueDepthMetric)
	c.cpuCacheUsageMetric = orDefault(cfg.CPUCacheUsageMetric, defaultCPUCacheUsageMetric)
	c.runningRequestsMetric = orDefault(cfg.RunningRequestsMetric, defaultRunningRequestsMetric)
	return c, nil
}

func (c *RouterMetricsCollector) TypedName() plugin.TypedName       { return c.typedName }
func (c *RouterMetricsCollector) CollectorFrequency() time.Duration { return c.interval }

// Poll scrapes each model with a metrics endpoint, sequentially. Failures are
// logged and leave the prior attribute in place (stale, not zeroed).
//
// TODO: scrape targets concurrently (bounded by maxConcurrent) once a
// deployment fronts enough pools that sequential polling lags.
func (c *RouterMetricsCollector) Poll(ctx context.Context) (any, error) {
	logger := log.FromContext(ctx).WithName("router-metrics-collector")

	for _, t := range c.collectTargets() {
		scrapeCtx, cancel := context.WithTimeout(ctx, c.timeout)
		start := time.Now()
		m, reason, err := c.scrape(scrapeCtx, t.url)
		cancel()
		ippmetrics.RecordKVCacheScrapeDuration(t.model, time.Since(start))
		if err != nil {
			logger.Error(err, "scrape failed", "model", t.model, "url", t.url, "reason", reason)
			ippmetrics.RecordKVCacheScrapeFailure(t.model, reason)
			continue
		}
		m.LastObservedAt = time.Now().UnixNano()
		recordMetrics(t.model, m)
		c.ds.GetOrCreateModel(t.model).GetAttributes().Put(datalayer.RouterMetricsAttributeKey, m)
	}
	return nil, nil
}

type target struct {
	model string
	url   string
}

// collectTargets returns (model, url) pairs for models that have a metrics endpoint configured.
// Models without a MetricsEndpoint attribute are not inference-pool targets and are skipped.
func (c *RouterMetricsCollector) collectTargets() []target {
	var targets []target
	for _, m := range c.ds.GetModels(func(m datalayer.Model) bool {
		_, ok := m.GetAttributes().Get(datalayer.MetricsEndpointAttributeKey)
		return ok
	}) {
		ep, err := datalayer.ReadAttributeKey[datalayer.MetricsEndpoint](m.GetAttributes(), datalayer.MetricsEndpointAttributeKey)
		if err == nil && ep.URL != "" {
			targets = append(targets, target{m.GetName(), ep.URL})
		}
	}
	return targets
}

// scrape fetches url and returns parsed metrics, or a failReason* label + error.
func (c *RouterMetricsCollector) scrape(ctx context.Context, url string) (datalayer.RouterMetrics, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return datalayer.RouterMetrics{}, failReasonRequestBuild, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return datalayer.RouterMetrics{}, failReasonTimeout, err
		}
		return datalayer.RouterMetrics{}, failReasonDial, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return datalayer.RouterMetrics{}, failReasonHTTPStatus, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// LegacyValidation permits colon names like "vllm:kv_cache_usage_perc".
	parser := expfmt.NewTextParser(model.LegacyValidation)
	fams, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		return datalayer.RouterMetrics{}, failReasonParse, err
	}
	// max collapses ratio gauges (hottest engine wins); sum collapses counts.
	util, _ := gaugeStats(fams, c.utilizationMetric)
	cpu, _ := gaugeStats(fams, c.cpuCacheUsageMetric)
	_, queue := gaugeStats(fams, c.queueDepthMetric)
	_, running := gaugeStats(fams, c.runningRequestsMetric)
	return datalayer.RouterMetrics{
		KVCacheUtilization:  util,
		CPUCacheUtilization: cpu,
		WaitingRequests:     int64(queue),
		RunningRequests:     int64(running),
	}, "", nil
}

func recordMetrics(modelName string, m datalayer.RouterMetrics) {
	ippmetrics.RecordKVCacheUtilization(modelName, m.KVCacheUtilization)
	ippmetrics.RecordKVCacheCPUUsage(modelName, m.CPUCacheUtilization)
	ippmetrics.RecordKVCacheQueueDepth(modelName, m.WaitingRequests)
	ippmetrics.RecordKVCacheRunningRequests(modelName, m.RunningRequests)
}

func parsePositive(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("%v must be positive", d)
	}
	return d, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// gaugeStats returns the peak and total of all gauge samples in the named family.
func gaugeStats(fams map[string]*dto.MetricFamily, name string) (peak, total float64) {
	fam := fams[name]
	if fam == nil {
		return 0, 0
	}
	for _, m := range fam.Metric {
		if m.Gauge == nil {
			continue
		}
		v := m.Gauge.GetValue()
		total += v
		if v > peak {
			peak = v
		}
	}
	return peak, total
}
