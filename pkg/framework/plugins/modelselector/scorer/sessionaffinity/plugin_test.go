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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

func newTestScorer(t *testing.T) *SessionAffinityScorer {
	t.Helper()
	p, err := ScorerFactory("test-sa", nil, nil)
	require.NoError(t, err)
	s, ok := p.(*SessionAffinityScorer)
	require.True(t, ok)
	return s
}

func testModels(names ...string) []datalayer.Model {
	models := make([]datalayer.Model, len(names))
	for i, name := range names {
		models[i] = datalayer.NewModel(name)
	}
	return models
}

// --- Factory tests ---

// Verify default config values are applied when no parameters are given.
func TestFactory_DefaultConfig(t *testing.T) {
	s := newTestScorer(t)
	assert.Equal(t, defaultSessionIDKey, s.sessionIDKey)
	assert.Equal(t, defaultMaxSessions, s.maxSessions)
	assert.Equal(t, PluginType, s.TypedName().Type)
	assert.Equal(t, "test-sa", s.TypedName().Name)
}

// Verify custom parameters override defaults.
func TestFactory_CustomConfig(t *testing.T) {
	raw := json.RawMessage(`{"sessionIdKey":"x-custom-id","maxSessions":500,"ttlSeconds":300}`)
	p, err := ScorerFactory("custom", raw, nil)
	require.NoError(t, err)
	s := p.(*SessionAffinityScorer)
	assert.Equal(t, "x-custom-id", s.sessionIDKey)
	assert.Equal(t, 500, s.maxSessions)
}

// Verify factory rejects malformed JSON.
func TestFactory_InvalidJSON(t *testing.T) {
	raw := json.RawMessage(`{invalid}`)
	_, err := ScorerFactory("bad", raw, nil)
	assert.Error(t, err)
}

// --- Score tests ---

// No session ID in request headers → 0.0 for all models.
func TestScore_NoSessionID_NoOpinion(t *testing.T) {
	s := newTestScorer(t)
	req := requesthandling.NewInferenceRequest()
	cs := plugin.NewCycleState()
	models := testModels("model-a", "model-b", "model-c")

	scores := s.Score(context.Background(), cs, req, models)

	require.Len(t, scores, 3)
	for _, m := range models {
		assert.Equal(t, noOpinionScore, scores[m],
			"no session ID should give no-opinion score to %s", m.GetName())
	}
}

// Session ID present but no prior mapping → 0.0 for all (first turn).
func TestScore_FirstTurn_NoOpinion(t *testing.T) {
	s := newTestScorer(t)
	req := requesthandling.NewInferenceRequest()
	req.Headers["x-session-id"] = "sess-new"
	cs := plugin.NewCycleState()
	models := testModels("model-a", "model-b")

	scores := s.Score(context.Background(), cs, req, models)

	require.Len(t, scores, 2)
	for _, m := range models {
		assert.Equal(t, noOpinionScore, scores[m],
			"first turn should give no-opinion score to %s", m.GetName())
	}
}

// Follow-up turn with known model in candidates → 1.0 for it, 0.0 for others.
func TestScore_FollowUpTurn_PrefersKnownModel(t *testing.T) {
	s := newTestScorer(t)
	s.cache.Add("sess-123", "model-b")

	req := requesthandling.NewInferenceRequest()
	req.Headers["x-session-id"] = "sess-123"
	cs := plugin.NewCycleState()
	models := testModels("model-a", "model-b", "model-c")

	scores := s.Score(context.Background(), cs, req, models)

	require.Len(t, scores, 3)
	for _, m := range models {
		if m.GetName() == "model-b" {
			assert.Equal(t, preferredScore, scores[m])
		} else {
			assert.Equal(t, noOpinionScore, scores[m])
		}
	}
}

// Known model no longer in candidates → 0.0 for all (graceful degradation).
func TestScore_KnownModelNoLongerInCandidates_NoOpinion(t *testing.T) {
	s := newTestScorer(t)
	s.cache.Add("sess-123", "model-removed")

	req := requesthandling.NewInferenceRequest()
	req.Headers["x-session-id"] = "sess-123"
	cs := plugin.NewCycleState()
	models := testModels("model-a", "model-b")

	scores := s.Score(context.Background(), cs, req, models)

	require.Len(t, scores, 2)
	for _, m := range models {
		assert.Equal(t, noOpinionScore, scores[m],
			"removed model should give no-opinion score to %s", m.GetName())
	}
}

// Empty model list → empty scores map.
func TestScore_EmptyModels(t *testing.T) {
	s := newTestScorer(t)
	req := requesthandling.NewInferenceRequest()
	req.Headers["x-session-id"] = "sess-1"
	cs := plugin.NewCycleState()

	scores := s.Score(context.Background(), cs, req, nil)

	assert.Empty(t, scores)
}

// Session ID is stored in CycleState for the ResponseProcessor.
func TestScore_StoresSessionIDInCycleState(t *testing.T) {
	s := newTestScorer(t)
	req := requesthandling.NewInferenceRequest()
	req.Headers["x-session-id"] = "my-session"
	cs := plugin.NewCycleState()
	models := testModels("model-a")

	s.Score(context.Background(), cs, req, models)

	val, err := plugin.ReadCycleStateKey[string](cs, cycleStateSessionIDKey)
	require.NoError(t, err)
	assert.Equal(t, "my-session", val)
}

// Session ID lookup uses the configured custom key.
func TestScore_UsesCustomSessionIDKey(t *testing.T) {
	raw := json.RawMessage(`{"sessionIdKey":"x-conv-id"}`)
	p, err := ScorerFactory("custom-key", raw, nil)
	require.NoError(t, err)
	s := p.(*SessionAffinityScorer)
	s.cache.Add("conv-123", "model-a")

	req := requesthandling.NewInferenceRequest()
	req.Headers["x-conv-id"] = "conv-123"
	cs := plugin.NewCycleState()
	models := testModels("model-a", "model-b")

	scores := s.Score(context.Background(), cs, req, models)

	assert.Equal(t, preferredScore, scores[models[0]])
	assert.Equal(t, noOpinionScore, scores[models[1]])
}

// --- ResponseProcessor tests ---

// Records a new session-to-model mapping on first turn.
func TestProcessResponse_RecordsMapping(t *testing.T) {
	s := newTestScorer(t)
	cs := plugin.NewCycleState()
	cs.Write(cycleStateSessionIDKey, "sess-456")

	resp := requesthandling.NewInferenceResponse()
	resp.Body[responseModelField] = "model-a"

	err := s.ProcessResponse(context.Background(), cs, resp)
	require.NoError(t, err)

	model, ok := s.cache.Get("sess-456")
	assert.True(t, ok)
	assert.Equal(t, "model-a", model)
}

// No session ID in CycleState → generates optimistic UUID and stores mapping.
func TestProcessResponse_NoSessionID_GeneratesOptimistic(t *testing.T) {
	s := newTestScorer(t)
	cs := plugin.NewCycleState()

	resp := requesthandling.NewInferenceResponse()
	resp.Body[responseModelField] = "model-a"

	err := s.ProcessResponse(context.Background(), cs, resp)
	require.NoError(t, err)

	assert.Equal(t, 1, s.cache.Len(), "should have stored optimistic session")

	// Session ID should be echoed as response header using default key
	echoedID := resp.Headers[defaultSessionIDKey]
	assert.NotEmpty(t, echoedID, "optimistic session ID should be echoed in response header")
	assert.Contains(t, resp.MutatedHeaders(), defaultSessionIDKey)

	// Verify the echoed ID matches what's in the cache
	model, ok := s.cache.Get(echoedID)
	assert.True(t, ok)
	assert.Equal(t, "model-a", model)
}

// No model in response body → no-op (no optimistic ID generated either).
func TestProcessResponse_NoModelInBody_Skips(t *testing.T) {
	s := newTestScorer(t)
	cs := plugin.NewCycleState()
	cs.Write(cycleStateSessionIDKey, "sess-789")

	resp := requesthandling.NewInferenceResponse()

	err := s.ProcessResponse(context.Background(), cs, resp)
	require.NoError(t, err)

	assert.Equal(t, 0, s.cache.Len())
}

// Updates mapping when model changes for an existing session.
func TestProcessResponse_UpdatesExistingMapping(t *testing.T) {
	s := newTestScorer(t)
	s.cache.Add("sess-1", "model-old")

	cs := plugin.NewCycleState()
	cs.Write(cycleStateSessionIDKey, "sess-1")

	resp := requesthandling.NewInferenceResponse()
	resp.Body[responseModelField] = "model-new"

	err := s.ProcessResponse(context.Background(), cs, resp)
	require.NoError(t, err)

	model, ok := s.cache.Get("sess-1")
	assert.True(t, ok)
	assert.Equal(t, "model-new", model)
}

// Same model as already stored → cache unchanged.
func TestProcessResponse_SameModel_NoUpdate(t *testing.T) {
	s := newTestScorer(t)
	s.cache.Add("sess-1", "model-a")

	cs := plugin.NewCycleState()
	cs.Write(cycleStateSessionIDKey, "sess-1")

	resp := requesthandling.NewInferenceResponse()
	resp.Body[responseModelField] = "model-a"

	err := s.ProcessResponse(context.Background(), cs, resp)
	require.NoError(t, err)

	model, ok := s.cache.Get("sess-1")
	assert.True(t, ok)
	assert.Equal(t, "model-a", model)
}

// Session ID is echoed back as a response header using the configured key.
func TestProcessResponse_EchoesSessionID(t *testing.T) {
	s := newTestScorer(t)
	cs := plugin.NewCycleState()
	cs.Write(cycleStateSessionIDKey, "sess-echo")

	resp := requesthandling.NewInferenceResponse()
	resp.Body[responseModelField] = "model-a"

	err := s.ProcessResponse(context.Background(), cs, resp)
	require.NoError(t, err)

	assert.Equal(t, "sess-echo", resp.Headers["x-session-id"])
	assert.Contains(t, resp.MutatedHeaders(), "x-session-id")
}

// --- End-to-end: Score → ProcessResponse → Score ---

// Full lifecycle: first turn (no opinion) → response records → second turn (prefers).
func TestEndToEnd_FirstTurnThenFollowUp(t *testing.T) {
	s := newTestScorer(t)
	models := testModels("model-a", "model-b")

	// Turn 1: session ID present but no prior model → no opinion
	req1 := requesthandling.NewInferenceRequest()
	req1.Headers["x-session-id"] = "conv-abc"
	cs1 := plugin.NewCycleState()

	scores1 := s.Score(context.Background(), cs1, req1, models)
	for _, m := range models {
		assert.Equal(t, noOpinionScore, scores1[m])
	}

	// Simulate model selection + response: model-b was picked
	resp1 := requesthandling.NewInferenceResponse()
	resp1.Body[responseModelField] = "model-b"
	err := s.ProcessResponse(context.Background(), cs1, resp1)
	require.NoError(t, err)

	// Turn 2: same session → prefers model-b
	req2 := requesthandling.NewInferenceRequest()
	req2.Headers["x-session-id"] = "conv-abc"
	cs2 := plugin.NewCycleState()

	scores2 := s.Score(context.Background(), cs2, req2, models)
	assert.Equal(t, noOpinionScore, scores2[models[0]]) // model-a
	assert.Equal(t, preferredScore, scores2[models[1]]) // model-b
}

// Optimistic ID end-to-end: no session ID → ID generated → client echoes it → affinity works.
func TestEndToEnd_OptimisticSessionID(t *testing.T) {
	s := newTestScorer(t)
	models := testModels("model-a", "model-b")

	// Turn 1: no session ID at all
	req1 := requesthandling.NewInferenceRequest()
	cs1 := plugin.NewCycleState()

	scores1 := s.Score(context.Background(), cs1, req1, models)
	for _, m := range models {
		assert.Equal(t, noOpinionScore, scores1[m])
	}

	// Response generates optimistic session ID
	resp1 := requesthandling.NewInferenceResponse()
	resp1.Body[responseModelField] = "model-b"
	err := s.ProcessResponse(context.Background(), cs1, resp1)
	require.NoError(t, err)

	generatedID := resp1.Headers["x-session-id"]
	require.NotEmpty(t, generatedID, "optimistic session ID should be set")

	// Turn 2: client echoes back the generated session ID → affinity kicks in
	req2 := requesthandling.NewInferenceRequest()
	req2.Headers["x-session-id"] = generatedID
	cs2 := plugin.NewCycleState()

	scores2 := s.Score(context.Background(), cs2, req2, models)
	assert.Equal(t, noOpinionScore, scores2[models[0]]) // model-a
	assert.Equal(t, preferredScore, scores2[models[1]]) // model-b
}

// --- LRU eviction tests ---

// LRU eviction removes the least recently used entry when at capacity.
func TestEviction_LRU_RemovesLeastRecent(t *testing.T) {
	raw := json.RawMessage(`{"maxSessions":3}`)
	p, err := ScorerFactory("evict-test", raw, nil)
	require.NoError(t, err)
	s := p.(*SessionAffinityScorer)

	s.cache.Add("sess-a", "model-1")
	s.cache.Add("sess-b", "model-2")
	s.cache.Add("sess-c", "model-3")

	// Access sess-a to make it recently used
	s.cache.Get("sess-a")

	// Add sess-d → should evict sess-b (least recently used)
	s.cache.Add("sess-d", "model-4")

	_, okA := s.cache.Get("sess-a")
	_, okB := s.cache.Get("sess-b")
	_, okD := s.cache.Get("sess-d")
	assert.True(t, okA, "sess-a was accessed recently, should survive")
	assert.False(t, okB, "sess-b was least recently used, should be evicted")
	assert.True(t, okD, "sess-d was just added, should be present")
}

// --- TTL tests ---

// Get does not return entries after TTL has expired.
func TestTTL_GetDoesNotReturnExpiredEntry(t *testing.T) {
	raw := json.RawMessage(`{"maxSessions":100,"ttlSeconds":1}`)
	p, err := ScorerFactory("ttl-test", raw, nil)
	require.NoError(t, err)
	s := p.(*SessionAffinityScorer)

	s.cache.Add("sess-ttl", "model-a")

	model, ok := s.cache.Get("sess-ttl")
	assert.True(t, ok, "entry should be found before TTL expires")
	assert.Equal(t, "model-a", model)

	time.Sleep(1100 * time.Millisecond)

	_, ok = s.cache.Get("sess-ttl")
	assert.False(t, ok, "entry should not be found after TTL expires")
}

// --- Capacity tests ---

// Cache respects maxSessions limit.
func TestCapacity_DoesNotExceedMax(t *testing.T) {
	raw := json.RawMessage(`{"maxSessions":10}`)
	p, err := ScorerFactory("cap-test", raw, nil)
	require.NoError(t, err)
	s := p.(*SessionAffinityScorer)

	for i := 0; i < 20; i++ {
		s.cache.Add(fmt.Sprintf("sess-%d", i), "model-a")
	}

	assert.LessOrEqual(t, s.cache.Len(), 10, "cache should not exceed maxSessions")
}
