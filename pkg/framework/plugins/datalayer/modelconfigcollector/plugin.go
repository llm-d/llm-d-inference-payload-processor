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

// ModelsConfig is the schema of the JSON config file.
type ModelsConfig struct {
	Models []ModelConfiguration `json:"models"`
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

	if err := c.syncModels(ctx); err != nil {
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
				if absEvent != c.absModelsPath {
					continue
				}
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
					if err := c.syncModels(ctx); err != nil {
						logger.Error(err, "failed to sync models after file change")
					}
				} else if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
					logger.Info("models config file removed or renamed; waiting for replacement", "path", c.absModelsPath)
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

// syncModels reads the config file, registers every valid listed model in the datastore,
// and removes any datastore model that no longer appears in the file.
func (c *ModelConfigDataSource) syncModels(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("model-config-datasource")

	data, err := os.ReadFile(c.absModelsPath)
	if err != nil {
		return err
	}

	var cfg ModelsConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		logger.Error(err, "failed to parse models config", "raw", string(data))
		return err
	}

	desired := make(map[string]struct{}, len(cfg.Models))
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
			continue
		}
		desired[m.Name] = struct{}{}
		mdl := c.ds.GetOrCreateModel(m.Name)
		mdl.GetAttributes().Put(pricing.TokenPricesAttributeKey, pricing.ToTokenPrices(m.Pricing))
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
