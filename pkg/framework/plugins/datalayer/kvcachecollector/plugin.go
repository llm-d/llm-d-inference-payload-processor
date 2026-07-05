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

package kvcachecollector

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
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/metricsendpoint"
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
	PluginType                 = "kv-cache-collector"
	KVCacheMetricsAttributeKey = "kv-cache-metrics"

	defaultInterval = 30 * time.Second
	defaultTimeout  = 5 * time.Second
	// Older vLLM uses "vllm:gpu_cache_usage_perc"; override via utilizationMetric.
	defaultUtilizationMetric     = "vllm:kv_cache_usage_perc"
	defaultQueueDepthMetric      = "vllm:num_requests_waiting"
	defaultCPUCacheUsageMetric   = "vllm:cpu_cache_usage_perc"
	defaultRunningRequestsMetric = "vllm:num_requests_running"
)

var _ dlsrc.Collector = &KVCacheCollector{}

// KVCacheMetrics is the per-model attribute written after a successful scrape.
// A failed scrape leaves the prior value; consumers gate freshness on LastObservedAt.
type KVCacheMetrics struct {
	Utilization     float64 // KV-cache pressure in [0, 1]
	QueueDepth      int64   // waiting requests
	CPUCacheUsage   float64 // CPU-cache pressure in [0, 1]
	RunningRequests int64   // running requests
	LastObservedAt  int64   // unix-nanos of last successful scrape; 0 if never
}

func (m KVCacheMetrics) Clone() datalayer.Cloneable { return m }

// CollectorConfig is the optional JSON config; empty fields use defaults.
type CollectorConfig struct {
	Interval              string `json:"interval,omitempty"`
	Timeout               string `json:"timeout,omitempty"`
	UtilizationMetric     string `json:"utilizationMetric,omitempty"`
	QueueDepthMetric      string `json:"queueDepthMetric,omitempty"`
	CPUCacheUsageMetric   string `json:"cpuCacheUsageMetric,omitempty"`
	RunningRequestsMetric string `json:"runningRequestsMetric,omitempty"`
}

// KVCacheCollector polls model metricsURLs; Poll runs serially so state needs no lock.
type KVCacheCollector struct {
	typedName  plugin.TypedName
	ds         datalayer.Datastore
	httpClient *http.Client

	interval              time.Duration
	timeout               time.Duration
	utilizationMetric     string
	queueDepthMetric      string
	cpuCacheUsageMetric   string
	runningRequestsMetric string
}

// NewKVCacheCollector returns a collector with defaults.
func NewKVCacheCollector(ds datalayer.Datastore) *KVCacheCollector {
	return &KVCacheCollector{
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

// CollectorFactory parses and validates JSON config into a KVCacheCollector.
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

	c := NewKVCacheCollector(h.Datastore())
	c.typedName.Name = name
	c.interval = interval
	c.timeout = timeout
	c.utilizationMetric = orDefault(cfg.UtilizationMetric, defaultUtilizationMetric)
	c.queueDepthMetric = orDefault(cfg.QueueDepthMetric, defaultQueueDepthMetric)
	c.cpuCacheUsageMetric = orDefault(cfg.CPUCacheUsageMetric, defaultCPUCacheUsageMetric)
	c.runningRequestsMetric = orDefault(cfg.RunningRequestsMetric, defaultRunningRequestsMetric)
	return c, nil
}

func (c *KVCacheCollector) TypedName() plugin.TypedName       { return c.typedName }
func (c *KVCacheCollector) CollectorFrequency() time.Duration { return c.interval }

// Poll scrapes each model with a metrics endpoint, sequentially. Failures are
// logged and leave the prior attribute in place (stale, not zeroed).
//
// TODO: scrape targets concurrently (bounded by a configurable maxConcurrent)
// once a deployment fronts enough pools that sequential polling lags.
func (c *KVCacheCollector) Poll(ctx context.Context) (any, error) {
	logger := log.FromContext(ctx).WithName("kv-cache-collector")

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
		c.ds.GetOrCreateModel(t.model).GetAttributes().Put(KVCacheMetricsAttributeKey, m)
	}
	return nil, nil
}

type target struct {
	model string
	url   string
}

// collectTargets snapshots (model, url) for models with a metrics endpoint.
func (c *KVCacheCollector) collectTargets() []target {
	var targets []target
	for _, m := range c.ds.GetModels(func(datalayer.Model) bool { return true }) {
		v, ok := m.GetAttributes().Get(metricsendpoint.AttributeKey)
		if !ok {
			continue
		}
		if ep, ok := v.(metricsendpoint.MetricsEndpoint); ok && ep.URL != "" {
			targets = append(targets, target{m.GetName(), ep.URL})
		}
	}
	return targets
}

// scrape fetches url and returns parsed metrics, or a failReason* label + error.
func (c *KVCacheCollector) scrape(ctx context.Context, url string) (KVCacheMetrics, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return KVCacheMetrics{}, failReasonRequestBuild, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return KVCacheMetrics{}, failReasonTimeout, err
		}
		return KVCacheMetrics{}, failReasonDial, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return KVCacheMetrics{}, failReasonHTTPStatus, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// LegacyValidation permits colon names like "vllm:kv_cache_usage_perc".
	parser := expfmt.NewTextParser(model.LegacyValidation)
	fams, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		return KVCacheMetrics{}, failReasonParse, err
	}
	// max collapses ratio gauges (hottest engine wins); sum collapses counts.
	util, _ := gaugeStats(fams, c.utilizationMetric)
	cpu, _ := gaugeStats(fams, c.cpuCacheUsageMetric)
	_, queue := gaugeStats(fams, c.queueDepthMetric)
	_, running := gaugeStats(fams, c.runningRequestsMetric)
	return KVCacheMetrics{
		Utilization:     util,
		QueueDepth:      int64(queue),
		CPUCacheUsage:   cpu,
		RunningRequests: int64(running),
	}, "", nil
}

func recordMetrics(model string, m KVCacheMetrics) {
	ippmetrics.RecordKVCacheUtilization(model, m.Utilization)
	ippmetrics.RecordKVCacheCPUUsage(model, m.CPUCacheUsage)
	ippmetrics.RecordKVCacheQueueDepth(model, m.QueueDepth)
	ippmetrics.RecordKVCacheRunningRequests(model, m.RunningRequests)
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
