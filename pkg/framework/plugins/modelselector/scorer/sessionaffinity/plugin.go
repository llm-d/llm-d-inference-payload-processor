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

package sessionaffinity

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/prometheus/client_golang/prometheus"
	compbasemetrics "k8s.io/component-base/metrics"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	metricsutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/metrics"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

const (
	PluginType = "session-affinity"

	defaultMaxSessions = 10000
	defaultTTL         = time.Hour

	cycleStateSessionIDKey = "session-affinity/session-id"

	responseModelField = "model"

	noOpinionScore = 0.0
	preferredScore = 1.0
)

const defaultSessionIDKey = "x-session-id"

var (
	_ modelselector.Scorer              = &SessionAffinityScorer{}
	_ requesthandling.ResponseProcessor = &SessionAffinityScorer{}
)

// --- Prometheus metrics ---

var (
	sessionCacheHits = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: "ipp",
			Name:      "session_affinity_cache_hits_total",
			Help:      metricsutil.HelpMsgWithStability("Count of session affinity cache hits.", compbasemetrics.ALPHA),
		},
		[]string{},
	)

	sessionCacheMisses = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: "ipp",
			Name:      "session_affinity_cache_misses_total",
			Help:      metricsutil.HelpMsgWithStability("Count of session affinity cache misses.", compbasemetrics.ALPHA),
		},
		[]string{},
	)

	sessionCacheSize = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: "ipp",
			Name:      "session_affinity_cache_size",
			Help:      metricsutil.HelpMsgWithStability("Current number of entries in the session affinity cache.", compbasemetrics.ALPHA),
		},
		[]string{},
	)

	sessionCacheEvictions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: "ipp",
			Name:      "session_affinity_cache_evictions_total",
			Help:      metricsutil.HelpMsgWithStability("Count of session affinity cache evictions.", compbasemetrics.ALPHA),
		},
		[]string{},
	)
)

func init() {
	metrics.Registry.MustRegister(sessionCacheHits)
	metrics.Registry.MustRegister(sessionCacheMisses)
	metrics.Registry.MustRegister(sessionCacheSize)
	metrics.Registry.MustRegister(sessionCacheEvictions)
}

// SessionAffinityScorerConfig defines the JSON configuration for the plugin.
type SessionAffinityScorerConfig struct {
	SessionIDKey string `json:"sessionIdKey"`
	MaxSessions  int    `json:"maxSessions"`
	TTLSeconds   int    `json:"ttlSeconds"`
}

// ScorerFactory creates a new SessionAffinityScorer from config.
func ScorerFactory(name string, rawParameters json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	var config SessionAffinityScorerConfig

	if len(rawParameters) > 0 {
		if err := json.Unmarshal(rawParameters, &config); err != nil {
			return nil, fmt.Errorf("failed to parse parameters for %q plugin: %w", PluginType, err)
		}
	}

	if config.SessionIDKey == "" {
		config.SessionIDKey = defaultSessionIDKey
	}
	if config.MaxSessions <= 0 {
		config.MaxSessions = defaultMaxSessions
	}

	ttl := defaultTTL
	if config.TTLSeconds > 0 {
		ttl = time.Duration(config.TTLSeconds) * time.Second
	}

	onEvict := func(_ string, _ string) {
		sessionCacheEvictions.WithLabelValues().Inc()
	}

	return &SessionAffinityScorer{
		typedName: plugin.TypedName{
			Type: PluginType,
			Name: name,
		},
		sessionIDKey: config.SessionIDKey,
		maxSessions:  config.MaxSessions,
		cache:        expirable.NewLRU[string, string](config.MaxSessions, onEvict, ttl),
	}, nil
}

// SessionAffinityScorer biases model selection toward the model that was
// previously selected for a given session. It tracks session-to-model mappings
// in an LRU cache with TTL via the ResponseProcessor interface.
//
// Scoring:
//   - No session ID found in request: 0.0 for all models (no opinion).
//   - Session ID found but no prior model recorded (first turn): 0.0 for all.
//   - Session ID found with a known prior model still in candidates: 1.0 for
//     that model, 0.0 for all others.
//   - Prior model no longer in candidates: 0.0 for all (no opinion).
type SessionAffinityScorer struct {
	typedName    plugin.TypedName
	sessionIDKey string
	maxSessions  int
	cache        *expirable.LRU[string, string]
}

// TypedName returns the type and name tuple of this plugin instance.
func (s *SessionAffinityScorer) TypedName() plugin.TypedName {
	return s.typedName
}

// Score returns a score in [0,1] for each model based on session affinity.
func (s *SessionAffinityScorer) Score(ctx context.Context, cycleState *plugin.CycleState, request *requesthandling.InferenceRequest, models []datalayer.Model) map[datalayer.Model]float64 {
	logger := log.FromContext(ctx)
	scores := make(map[datalayer.Model]float64, len(models))

	sessionID := request.Headers[s.sessionIDKey]
	if sessionID == "" {
		for _, m := range models {
			scores[m] = noOpinionScore
		}
		logger.V(logutil.VERBOSE).Info("no session ID found, no opinion")
		return scores
	}

	cycleState.Write(cycleStateSessionIDKey, sessionID)

	previousModel, known := s.cache.Get(sessionID)

	if !known {
		for _, m := range models {
			scores[m] = noOpinionScore
		}
		sessionCacheMisses.WithLabelValues().Inc()
		logger.V(logutil.VERBOSE).Info("first turn for session, no opinion", "sessionId", sessionID)
		return scores
	}

	sessionCacheHits.WithLabelValues().Inc()

	var matched bool
	for _, m := range models {
		if m.GetName() == previousModel {
			scores[m] = preferredScore
			matched = true
		} else {
			scores[m] = noOpinionScore
		}
	}

	if !matched {
		logger.V(logutil.VERBOSE).Info("previous model not in candidates, no opinion",
			"previousModel", previousModel, "sessionId", sessionID)
		return scores
	}

	logger.V(logutil.VERBOSE).Info("session affinity applied",
		"preferredModel", previousModel, "sessionId", sessionID)
	return scores
}

// ProcessResponse records the session-to-model mapping so that future requests
// with the same session ID can be biased toward the same model. It also echoes
// the session ID back as a response header.
//
// When no session ID was present in the request, a new UUID is generated
// optimistically and echoed back — if the client sends it on the next request,
// session affinity will be established automatically.
func (s *SessionAffinityScorer) ProcessResponse(ctx context.Context, cycleState *plugin.CycleState, response *requesthandling.InferenceResponse) error {
	logger := log.FromContext(ctx)

	modelName, ok := response.Body[responseModelField].(string)
	if !ok || modelName == "" {
		logger.V(logutil.VERBOSE).Info("no model in response body, skipping response processing")
		return nil
	}

	sessionID, err := plugin.ReadCycleStateKey[string](cycleState, cycleStateSessionIDKey)
	if err != nil {
		// No session ID in request — generate one optimistically
		sessionID = uuid.New().String()
		logger.V(logutil.VERBOSE).Info("generated optimistic session ID",
			"sessionId", sessionID, "model", modelName)
	}

	s.cache.Add(sessionID, modelName)
	sessionCacheSize.WithLabelValues().Set(float64(s.cache.Len()))

	logger.V(logutil.VERBOSE).Info("recorded session affinity",
		"sessionId", sessionID, "model", modelName)

	response.SetHeader(s.sessionIDKey, sessionID)

	return nil
}
