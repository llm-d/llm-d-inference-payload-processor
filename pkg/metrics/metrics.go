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

package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	compbasemetrics "k8s.io/component-base/metrics"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	metricsutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/metrics"
)

const component = "ipp"

var (
	// --- Info Metrics ---
	ippInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: component,
			Name:      "info",
			Help:      metricsutil.HelpMsgWithStability("General information of the current build of IPP.", compbasemetrics.ALPHA),
		},
		[]string{"commit", "build_ref"},
	)

	successCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: component,
			Name:      "success_total",
			Help:      metricsutil.HelpMsgWithStability("Count of time the request was processed successfully.", compbasemetrics.ALPHA),
		},
		[]string{},
	)

	bodyFieldNotFoundCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: component,
			Name:      "body_field_not_found_total",
			Help:      metricsutil.HelpMsgWithStability("Count of times a field wasn't found in a request body.", compbasemetrics.ALPHA),
		},
		[]string{"field"},
	)

	bodyFieldEmptyCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: component,
			Name:      "body_field_empty_total",
			Help:      metricsutil.HelpMsgWithStability("Count of times a field was found in a request body but was empty.", compbasemetrics.ALPHA),
		},
		[]string{"field"},
	)

	pluginProcessingLatencies = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: component,
			Name:      "plugin_duration_seconds",
			Help:      metricsutil.HelpMsgWithStability("Plugin processing latency distribution in seconds for each extension point, plugin type and plugin name.", compbasemetrics.ALPHA),
			Buckets: []float64{
				0.0001, 0.0002, 0.0005, 0.001, 0.002, 0.005, 0.01, 0.02, 0.05, 0.1,
			},
		},
		[]string{"extension_point", "plugin_type", "plugin_name"},
	)

	modelSelectorE2ELatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: component,
			Name:      "model_selector_e2e_duration_seconds",
			Help:      metricsutil.HelpMsgWithStability("End-to-end model selection latency distribution in seconds.", compbasemetrics.ALPHA),
			Buckets: []float64{
				0.0001, 0.0002, 0.0005, 0.001, 0.002, 0.005, 0.01, 0.02, 0.05, 0.1,
			},
		},
		[]string{},
	)

	modelSelectorAttemptTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: component,
			Name:      "model_selector_attempt_total",
			Help:      metricsutil.HelpMsgWithStability("Count of model selection attempts by status.", compbasemetrics.ALPHA),
		},
		[]string{"status"},
	)

	requestTTFT = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: component,
			Name:      "request_ttft_seconds",
			Help:      metricsutil.HelpMsgWithStability("Time to first token distribution in seconds per model.", compbasemetrics.ALPHA),
			Buckets:   []float64{0.05, 0.1, 0.2, 0.5, 1.0, 2.0, 5.0, 10.0, 30.0},
		},
		[]string{"model"},
	)
	// --- router-metrics-collector observability ---
	kvCacheScrapeDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: component,
			Name:      "kv_cache_scrape_duration_seconds",
			Help:      metricsutil.HelpMsgWithStability("Latency of a single KV-cache metrics scrape, including failures.", compbasemetrics.ALPHA),
			Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
		},
		[]string{"model"},
	)

	kvCacheScrapeFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: component,
			Name:      "kv_cache_scrape_failures_total",
			Help:      metricsutil.HelpMsgWithStability("Number of failed KV-cache metrics scrapes by reason.", compbasemetrics.ALPHA),
		},
		[]string{"model", "reason"},
	)

	kvCacheUtilization = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: component,
			Name:      "kv_cache_utilization_ratio",
			Help:      metricsutil.HelpMsgWithStability("Last observed pool-wide KV-cache utilization per model (0.0-1.0).", compbasemetrics.ALPHA),
		},
		[]string{"model"},
	)

	kvCacheCPUUsage = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: component,
			Name:      "kv_cache_cpu_usage_ratio",
			Help:      metricsutil.HelpMsgWithStability("Last observed pool-wide CPU KV-cache utilization per model (0.0-1.0).", compbasemetrics.ALPHA),
		},
		[]string{"model"},
	)

	kvCacheQueueDepth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: component,
			Name:      "kv_cache_queue_depth",
			Help:      metricsutil.HelpMsgWithStability("Last observed sum of waiting requests across the pool per model.", compbasemetrics.ALPHA),
		},
		[]string{"model"},
	)

	kvCacheRunningRequests = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: component,
			Name:      "kv_cache_running_requests",
			Help:      metricsutil.HelpMsgWithStability("Last observed sum of running requests across the pool per model.", compbasemetrics.ALPHA),
		},
		[]string{"model"},
	)
)

var registerMetrics sync.Once

// Register all metrics.
func Register(customCollectors ...prometheus.Collector) {
	registerMetrics.Do(func() {
		metrics.Registry.MustRegister(ippInfo)
		metrics.Registry.MustRegister(successCounter)
		metrics.Registry.MustRegister(bodyFieldNotFoundCounter)
		metrics.Registry.MustRegister(bodyFieldEmptyCounter)
		metrics.Registry.MustRegister(pluginProcessingLatencies)
		metrics.Registry.MustRegister(modelSelectorE2ELatency)
		metrics.Registry.MustRegister(modelSelectorAttemptTotal)
		metrics.Registry.MustRegister(requestTTFT)
		metrics.Registry.MustRegister(kvCacheScrapeDuration)
		metrics.Registry.MustRegister(kvCacheScrapeFailures)
		metrics.Registry.MustRegister(kvCacheUtilization)
		metrics.Registry.MustRegister(kvCacheCPUUsage)
		metrics.Registry.MustRegister(kvCacheQueueDepth)
		metrics.Registry.MustRegister(kvCacheRunningRequests)
		for _, collector := range customCollectors {
			metrics.Registry.MustRegister(collector)
		}
	})
}

// RecordIPPInfo records ipp build info.
func RecordIPPInfo(commitSha, buildRef string) {
	ippInfo.WithLabelValues(commitSha, buildRef).Set(1)
}

// RecordSuccessCounter records the number of times the request was processed successfully.
func RecordSuccessCounter() {
	successCounter.WithLabelValues().Inc()
}

// RecordBodyFieldNotFound records the number of times a field wasn't found in a request body.
func RecordBodyFieldNotFound(fieldName string) {
	bodyFieldNotFoundCounter.WithLabelValues(fieldName).Inc()
}

// RecordBodyFieldEmpty records the number of times a field was found in a request body but was empty.
func RecordBodyFieldEmpty(fieldName string) {
	bodyFieldEmptyCounter.WithLabelValues(fieldName).Inc()
}

// RecordPluginProcessingLatency records the processing latency for an IPP plugin.
func RecordPluginProcessingLatency(extensionPoint, pluginType, pluginName string, duration time.Duration) {
	pluginProcessingLatencies.WithLabelValues(extensionPoint, pluginType, pluginName).Observe(duration.Seconds())
}

// RecordModelSelectorE2ELatency records the end-to-end latency of model selection.
func RecordModelSelectorE2ELatency(duration time.Duration) {
	modelSelectorE2ELatency.WithLabelValues().Observe(duration.Seconds())
}

// RecordRequestTTFT records the time-to-first-token for a completed request.
func RecordRequestTTFT(model string, duration time.Duration) {
	requestTTFT.WithLabelValues(model).Observe(duration.Seconds())
}

// RecordModelSelectorAttempt records a model selection attempt with success or failure status.
func RecordModelSelectorAttempt(err error) {
	if err != nil {
		modelSelectorAttemptTotal.WithLabelValues("failure").Inc()
		return
	}
	modelSelectorAttemptTotal.WithLabelValues("success").Inc()
}

// RecordKVCacheScrapeDuration records scrape latency; called for both successes and failures.
func RecordKVCacheScrapeDuration(model string, duration time.Duration) {
	kvCacheScrapeDuration.WithLabelValues(model).Observe(duration.Seconds())
}

// RecordKVCacheScrapeFailure records a failed scrape; reason must be a low-cardinality token.
func RecordKVCacheScrapeFailure(model, reason string) {
	kvCacheScrapeFailures.WithLabelValues(model, reason).Inc()
}

// RecordKVCacheUtilization records the last observed pool-wide GPU KV-cache utilization.
func RecordKVCacheUtilization(model string, value float64) {
	kvCacheUtilization.WithLabelValues(model).Set(value)
}

// RecordKVCacheCPUUsage records the last observed pool-wide CPU KV-cache utilization.
func RecordKVCacheCPUUsage(model string, value float64) {
	kvCacheCPUUsage.WithLabelValues(model).Set(value)
}

// RecordKVCacheQueueDepth records the last observed sum of waiting requests across the pool.
func RecordKVCacheQueueDepth(model string, value int64) {
	kvCacheQueueDepth.WithLabelValues(model).Set(float64(value))
}

// RecordKVCacheRunningRequests records the last observed sum of running requests across the pool.
func RecordKVCacheRunningRequests(model string, value int64) {
	kvCacheRunningRequests.WithLabelValues(model).Set(float64(value))
}
