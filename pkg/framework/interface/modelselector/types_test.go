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

package modelselector

import (
	"encoding/json"
	"testing"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
)

// TestScoredModel_MarshalJSON locks the ScoredModel marshaling contract:
// both the embedded Model's name and Score must appear in the JSON
// output. Without ScoredModel.MarshalJSON, Go promotes the embedded
// Model's MarshalJSON and Score is silently dropped turning the
// picker/pipeline "scoredModels" log lines into a name-only list with
// no visible scores.
func TestScoredModel_MarshalJSON(t *testing.T) {
	sm := &ScoredModel{Model: datalayer.NewModel("m1"), Score: 0.75}

	b, err := json.Marshal(sm)
	if err != nil {
		t.Fatalf("json.Marshal(ScoredModel): %v", err)
	}

	const want = `{"name":"m1","score":0.75}`
	if string(b) != want {
		t.Fatalf("got %s, want %s", b, want)
	}
}
