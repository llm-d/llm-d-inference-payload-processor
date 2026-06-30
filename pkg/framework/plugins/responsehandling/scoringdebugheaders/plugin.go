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

// Package scoringdebugheaders writes queue-ttft scoring decisions as response headers.
package scoringdebugheaders

import (
	"context"
	"encoding/json"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	queuettft "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/scorer/queuettft"
	modelselector "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/modelselector"
)

const (
	PluginType = "scoring-debug-headers"

	headerModel       = "X-IPP-Scorer-Model"
	headerState       = "X-IPP-Scorer-State"
	headerEffTTFT     = "X-IPP-Scorer-EffectiveTTFT"
	headerRealTTFT    = "X-IPP-Real-TTFT"
	headerFloor       = "X-IPP-Scorer-Floor"
	headerP50         = "X-IPP-Scorer-P50"
	headerRecentN     = "X-IPP-Scorer-RecentN"
	headerInflight    = "X-IPP-Scorer-Inflight"
	headerExploration = "X-IPP-Scorer-ExplorationSuppressed"
)

var _ requesthandling.ResponseProcessor = &ScoringDebugHeadersPlugin{}

func Factory(name string, _ json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	return NewScoringDebugHeadersPlugin().WithName(name), nil
}

func NewScoringDebugHeadersPlugin() *ScoringDebugHeadersPlugin {
	return &ScoringDebugHeadersPlugin{
		typedName: plugin.TypedName{Type: PluginType, Name: PluginType},
	}
}

type ScoringDebugHeadersPlugin struct {
	typedName plugin.TypedName
}

func (p *ScoringDebugHeadersPlugin) TypedName() plugin.TypedName { return p.typedName }
func (p *ScoringDebugHeadersPlugin) WithName(name string) *ScoringDebugHeadersPlugin {
	p.typedName.Name = name; return p
}

func (p *ScoringDebugHeadersPlugin) ProcessResponse(ctx context.Context, cycleState *plugin.CycleState, response *requesthandling.InferenceResponse) error {
	selectedModel, err := plugin.ReadCycleStateKey[string](cycleState, modelselector.SelectedModelCycleStateKey)
	if err != nil {
		log.FromContext(ctx).V(logutil.DEBUG).Info("scoring-debug-headers: no selected model in CycleState, skipping")
		return nil
	}

	decisions, err := plugin.ReadCycleStateKey[map[string]queuettft.ScoringDecision](cycleState, queuettft.DecisionsCycleStateKey)
	if err != nil {
		log.FromContext(ctx).V(logutil.DEBUG).Info("scoring-debug-headers: no scoring decisions in CycleState, skipping")
		return nil
	}

	d, ok := decisions[selectedModel]
	if !ok {
		log.FromContext(ctx).V(logutil.DEBUG).Info("scoring-debug-headers: selected model not in decisions", "model", selectedModel)
		return nil
	}

	response.SetHeader(headerModel, selectedModel)
	response.SetHeader(headerState, d.State)
	response.SetHeader(headerRecentN, fmt.Sprintf("%d", d.RecentN))
	response.SetHeader(headerInflight, fmt.Sprintf("%d", d.Inflight))

	if d.Floor > 0 {
		response.SetHeader(headerFloor, fmt.Sprintf("%.3f", d.Floor))
	}
	if d.P50 > 0 {
		response.SetHeader(headerP50, fmt.Sprintf("%.3f", d.P50))
	}
	if d.EffectiveTTFT > 0 {
		response.SetHeader(headerEffTTFT, fmt.Sprintf("%.3f", d.EffectiveTTFT))
	}
	if d.ExplorationSuppressed {
		response.SetHeader(headerExploration, "true")
	}

	if realTTFT, err := plugin.ReadCycleStateKey[float64](cycleState, requesthandling.TTFTCycleStateKey); err == nil && realTTFT > 0 {
		response.SetHeader(headerRealTTFT, fmt.Sprintf("%.3f", realTTFT))
	}

	return nil
}
