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

package modelconfigcollector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	dlsrc "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/datasource"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/modelgroups"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer/pricing"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
)

const PluginType = "model-config-datasource"

// compile-time interface assertion
var _ dlsrc.DataSource = &ModelConfigDataSource{}

// PluginConfig holds the JSON plugin configuration for this datasource.
type PluginConfig struct {
	ModelsPath string `json:"modelsPath"`
}

// ModelConfiguration is a single model entry in the config file.
//
// Pricing holds the per-million pricing block. When omitted from JSON, it defaults
// to the zero value (0 input, 0 output per million), registering the model as free.
// Prices are expressed in USD per 1,000,000 tokens and are converted to per-token
// prices (divided by 1e6) before storage as TokenPrices in the datastore.
type ModelConfiguration struct {
	Name    string                  `json:"name"`
	Pricing pricing.ModelPriceShape `json:"pricing"`
}

// ModelGroupConfig is a named group of model names in the config file. It is
// used to populate each listed model's modelgroups.GroupsAttributeKey attribute.
type ModelGroupConfig struct {
	Name   string   `json:"name"`
	Models []string `json:"models"`
}

// ModelsConfig is the schema of the JSON config file.
type ModelsConfig struct {
	Models []ModelConfiguration `json:"models"`
	Groups []ModelGroupConfig   `json:"groups,omitempty"`
}

// ModelConfigDataSource watches a JSON file listing model names and keeps the
// datastore in sync whenever the file changes.
type ModelConfigDataSource struct {
	typedName     plugin.TypedName
	ds            datalayer.Datastore
	absModelsPath string
	stopCh        chan struct{}
	doneCh        chan struct{}
}

// DatasourceFactory creates a ModelConfigDataSource from the plugin handle and raw JSON config.
// It validates that modelsPath is set and that the file exists; content parsing happens in Start.
func DatasourceFactory(name string, rawCfg json.RawMessage, h plugin.Handle) (plugin.Plugin, error) {
	var cfg PluginConfig
	if err := json.Unmarshal(rawCfg, &cfg); err != nil {
		return nil, err
	}
	if cfg.ModelsPath == "" {
		return nil, errors.New("modelsPath is required")
	}
	absPath, err := filepath.Abs(cfg.ModelsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve modelsPath %q: %w", cfg.ModelsPath, err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("modelsPath must be a file, not a directory")
	}
	return NewModelConfigDataSource(name, absPath, h.Datastore()), nil
}

// NewModelConfigDataSource constructs a ModelConfigDataSource wired to ds.
func NewModelConfigDataSource(name, modelsPath string, ds datalayer.Datastore) *ModelConfigDataSource {
	return &ModelConfigDataSource{
		typedName:     plugin.TypedName{Type: PluginType, Name: name},
		ds:            ds,
		absModelsPath: modelsPath,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
}

func (c *ModelConfigDataSource) TypedName() plugin.TypedName { return c.typedName }

// Start performs an initial sync from the config file, then launches a goroutine that
// watches the file's parent directory for changes and re-syncs on every relevant event.
// The directory is watched (rather than the file directly) to handle atomic
// rename-based replacements such as Kubernetes ConfigMap remounts.
func (c *ModelConfigDataSource) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("model-config-datasource")

	data, err := readModelsFile(c.absModelsPath, true)
	if err != nil {
		return fmt.Errorf("failed to read models config: %w", err)
	}
	if err := c.syncModels(ctx, data); err != nil {
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	dir := filepath.Dir(c.absModelsPath)
	if err := watcher.Add(dir); err != nil {
		watcher.Close() //nolint:errcheck
		return err
	}

	go func() {
		defer close(c.doneCh)
		defer watcher.Close() //nolint:errcheck

		for {
			select {
			case <-c.stopCh:
				return
			case <-ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				absEvent, err := filepath.Abs(event.Name)
				if err != nil {
					logger.Error(err, "failed to resolve event path", "path", event.Name)
					continue
				}
				// Verify that event refers to the config file
				if absEvent != c.absModelsPath {
					continue
				}
				// The following handles ONLY changes to the configuration file
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
					data, err := readModelsFile(c.absModelsPath, false)
					if err != nil {
						logger.Error(err, "failed to read models config after file change")
						continue
					}
					if err := c.syncModels(ctx, data); err != nil {
						logger.Error(err, "failed to sync models after file change")
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logger.Error(err, "fsnotify watcher error")
			}
		}
	}()

	return nil
}

// Stop signals the watcher goroutine to exit and blocks until it has fully stopped.
func (c *ModelConfigDataSource) Stop() {
	close(c.stopCh)
	<-c.doneCh
}

// readModelsFile reads the config file at path. If failOnMissing is false, a missing
// file is treated as empty content rather than an error, so callers can converge to an
// empty config when the file has been deleted or renamed away after startup.
func readModelsFile(path string, failOnMissing bool) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !failOnMissing {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

// syncModels registers every valid model listed in data in the datastore, removes any
// datastore model that no longer appears in data, and (re)populates each model's group
// membership from the config's group-centric "groups" list. Empty data (an empty or
// missing config file) is treated as an empty config, which clears every model from
// the datastore.
func (c *ModelConfigDataSource) syncModels(ctx context.Context, data []byte) error {
	logger := log.FromContext(ctx).WithName("model-config-datasource")

	var cfg ModelsConfig
	if len(data) > 0 {
		if err := json.Unmarshal(data, &cfg); err != nil {
			logger.Error(err, "failed to parse models config", "raw", string(data))
			return err
		}
	}

	// membership maps a model name to the group names it belongs to, inverting the
	// config's group-centric "groups" list ({name, models[]}) so it can be looked up
	// per model below. Membership is only resolved against the "models" list once
	// that loop below has run, so a group can reference a model before its validity
	// as a model entry is known.
	// A group with an empty name or an empty model list is invalid as the filter's
	// "auto/<group-name>" syntax requires non-empty group-name.
	// An individual empty model name within an otherwise valid group is
	// skipped on its own.
	membership := make(map[string]modelgroups.Groups)
	for _, g := range cfg.Groups {
		if g.Name == "" || len(g.Models) == 0 {
			logger.Info("skipping invalid group configuration", "group", g.Name, "models", g.Models)
			continue
		}
		for _, modelName := range g.Models {
			if modelName == "" {
				logger.Info("skipping empty model name in group configuration", "group", g.Name)
				continue
			}
			membership[modelName] = append(membership[modelName], g.Name)
		}
	}

	desired := make(map[string]struct{}, len(cfg.Models))
	invalid := make(map[string]struct{}, len(cfg.Models))
	for _, m := range cfg.Models {
		if m.Name == "" {
			logger.Info("skipping model entry with empty name")
			continue
		}
		if m.Pricing.InputPerMillion < 0 || m.Pricing.OutputPerMillion < 0 {
			logger.Info("skipping model entry with negative price",
				"model", m.Name,
				"input_per_million", m.Pricing.InputPerMillion,
				"output_per_million", m.Pricing.OutputPerMillion)
			invalid[m.Name] = struct{}{}
			continue
		}
		desired[m.Name] = struct{}{}
		mdl := c.ds.GetOrCreateModel(m.Name)
		mdl.GetAttributes().Put(pricing.TokenPricesAttributeKey, pricing.ToTokenPrices(m.Pricing))
		// Refresh group membership alongside pricing so a model that lost all its
		// group memberships in this reload doesn't keep a stale attribute.
		if groups, ok := membership[m.Name]; ok {
			mdl.GetAttributes().Put(modelgroups.GroupsAttributeKey, groups)
		} else {
			mdl.GetAttributes().Delete(modelgroups.GroupsAttributeKey)
		}
	}

	// A group may reference a model name that isn't a valid "models" entry: either
	// missing from "models" entirely, or present but itself skipped (empty name,
	// negative price). Group membership never auto-creates a model, so just warn,
	// distinguishing the two cases so the "invalid model" warning already logged
	// above isn't mistaken for an unrelated "unknown model" one.
	for modelName := range membership {
		if _, ok := desired[modelName]; ok {
			continue
		}
		if _, ok := invalid[modelName]; ok {
			logger.Info("skipping invalid model referenced in group configuration", "model", modelName)
		} else {
			logger.Info("skipping unknown model in group configuration", "model", modelName)
		}
	}

	for _, model := range c.ds.GetModels(datalayer.AllModelsPredicate) {
		modelName := model.GetName()
		if _, ok := desired[modelName]; !ok {
			logger.Info("removing model no longer present in config", "model", modelName)
			c.ds.DeleteModel(modelName)
		}
	}

	return nil
}
