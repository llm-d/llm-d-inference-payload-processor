# Extending IPP with a custom plugin

## Goal

This tutorial walks through writing a custom plugin for the Inference Payload Processor (IPP),
registering it so the configuration loader can instantiate it, and wiring it into a profile.

The worked example is [`body-field-to-header`][body-field-to-header-src], a small request-processing
plugin that copies a request-body field into an HTTP header. It exercises every part of the plugin
contract — a struct, a factory, parameter parsing, an extension-point method, and a `TypedName` — and
the same recipe applies to every other plugin kind.

For the pipeline model (profiles, ext-proc lifecycle, model selection, data layer) see
[Architecture][Architecture]; for the in-tree plugin catalogue and full configuration model see
[Plugins][Plugins].

## The plugin model

Every plugin implements the base [`plugin.Plugin`][plugin-src] interface, a single method:

```go
type Plugin interface {
    // TypedName returns the type and name tuple of this plugin instance.
    TypedName() TypedName
}
```

`TypedName` is a `{Type, Name}` tuple: `Type` is the registered type-name constant, `Name` is the
per-instance name from configuration. Because instances are named, one plugin type can be instantiated
multiple times with different parameters.

A plugin then **additionally implements** one or more extension-point interfaces; the loader inspects
which it satisfies and routes it to the matching pipeline stage or data-layer role. The interfaces are
defined in three packages:

| Interface | Package | Role |
|-----------|---------|------|
| `RequestProcessor` | [`requesthandling`][requesthandling-src] | Inspect and mutate the request before routing. Can be run as part of a profile, or prior to picking a profile. |
| `ResponseProcessor` | [`requesthandling`][requesthandling-src] | Inspect and mutate the response on its way back. |
| `ProfilePicker` | [`requesthandling`][requesthandling-src] | Choose which profile runs for a request. |
| `Filter` / `Scorer` / `Picker` | [`modelselector`][modelselector-src] | The `Filter → Score → Pick` phases that select a *model*. |
| `Collector` / `Extractor` / `DataSource` | [`datalayer/datasource`][datalayer-src] | Maintain cross-request state consumed by Filters and Scorers. |

This tutorial implements `RequestProcessor`; see [Other extension points](#other-extension-points)
for the rest.

## Code walkthrough

The example lives in [`body_field_to_header.go`][body-field-to-header-src]. The plugin declares its
registered type, a parameters struct, the plugin struct, and a compile-time interface assertion:

```go
const BodyFieldToHeaderPluginType = "body-field-to-header"

// compile-time check that the plugin satisfies the RequestProcessor interface
var _ requesthandling.RequestProcessor = &BodyFieldToHeaderPlugin{}

// BodyFieldToHeaderConfig is the JSON/YAML parameter shape.
type BodyFieldToHeaderConfig struct {
    FieldName  string `json:"fieldName"`
    HeaderName string `json:"headerName"`
}

type BodyFieldToHeaderPlugin struct {
    typedName  plugin.TypedName
    fieldName  string
    headerName string
}
```

Plugins are constructed by a **factory** matching the [`plugin.FactoryFunc`][registry-src] signature.
It receives the instance name, the raw parameters, and a [`plugin.Handle`][handle-src]; it parses the
parameters and stamps the configured name with `WithName`:

```go
func BodyFieldToHeaderPluginFactory(name string, rawParameters json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
    var config BodyFieldToHeaderConfig
    if len(rawParameters) > 0 {
        if err := json.Unmarshal(rawParameters, &config); err != nil {
            return nil, fmt.Errorf("failed to parse parameters of '%s': %w", BodyFieldToHeaderPluginType, err)
        }
    }
    plugin, err := NewBodyFieldToHeaderPlugin(config.FieldName, config.HeaderName) // validates inputs, seeds TypedName
    if err != nil {
        return nil, err
    }
    return plugin.WithName(name), nil
}
```

The extension-point method does the work. `RequestProcessor` requires:

```go
ProcessRequest(ctx context.Context, cycleState *plugin.CycleState, request *InferenceRequest) error
```

The implementation reads the body field and sets the header, treating an absent or empty field as a
no-op:

```go
func (p *BodyFieldToHeaderPlugin) ProcessRequest(ctx context.Context, _ *plugin.CycleState, request *requesthandling.InferenceRequest) error {
    rawFieldValue, exists := request.Body[p.fieldName]
    if !exists {
        metrics.RecordBodyFieldNotFound(p.fieldName)
        return nil
    }
    fieldStr := fmt.Sprintf("%v", rawFieldValue)
    if fieldStr == "" {
        metrics.RecordBodyFieldEmpty(p.fieldName)
        return nil
    }
    request.SetHeader(p.headerName, fieldStr)
    return nil
}
```

Key points about the contract:

- Plugins **mutate the request in place** rather than returning mutations. `request.SetHeader(...)`
  (and `SetBody`, `SetBodyField`, `RemoveHeader`, ...) record changes on the embedded
  [`InferenceMessage`][requesthandling-types-src]; the framework translates them into the ext-proc
  response the Proxy applies. Returning `nil` with no mutation is a valid no-op.
- A non-nil `error` aborts processing for that request.
- `cycleState` is a [per-request key/value store][cycle-state-src] for passing data between plugins in
  the same request (`Write`/`Read`, or the typed `plugin.ReadCycleStateKey[T]`). This plugin does not
  use it.

## Registering the plugin

A type must be registered before the loader can instantiate it. [`plugin.Register`][registry-src] maps
a type string to a factory; in-tree plugins register in `registerInTreePlugins` in
[`cmd/runner/runner.go`][runner-src]:

```go
func (r *Runner) registerInTreePlugins() {
    plugin.Register(bodyfieldtoheader.BodyFieldToHeaderPluginType, bodyfieldtoheader.BodyFieldToHeaderPluginFactory)
    // ...existing registrations...
}
```

The first argument is the string clients put under `type:` in the config; the second is the factory
the loader calls per configured instance.

## Configuring the plugin

Declare each plugin **once** under the top-level `plugins` list, then reference it by name from a
profile's `request` list with `pluginRef`:

```yaml
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- type: body-field-to-header
  name: model-to-header        # optional; defaults to the type
  parameters:
    fieldName: model
    headerName: X-Gateway-Model-Name
profiles:
- name: default
  plugins:
    request:
    - pluginRef: model-to-header
```

The `parameters` block is opaque to the framework — it is handed to the factory as raw JSON/YAML.
With a single profile and no `profilePicker`, [`single-profile-picker`] is enabled automatically. See
[Configuration][Configuration] for the full schema (pre/post processing, the `datalayer` section,
scorer `weight`, proxy integration).

## Additional extension points

Generally, plugins have the same shape but implements different interfaces. E.g., one of the
[`modelselector`][modelselector-src] interfaces (`Filter`, `Scorer`, or `Picker`) instead of
`RequestProcessor`. For example, a `Scorer` implements `Score(...) map[datalayer.Model]float64`,
returning a score per candidate.
Following is the available interfaces that can be implemented.

### Request-handling interfaces

In addition to `ProcessRequest`, there are additional request processing interfaces, such as **`ProfilePicker`** and **`PreProcess`**.

**`ProfilePicker`** — called once per request to select the profile to run. The implementation below
is the built-in [`single-profile-picker`][single-profile-picker-src], which asserts exactly one
profile is configured and returns it unconditionally:

```go
// Pick selects the Profile to run from the list of candidate profiles, while taking into
// consideration the request properties and the previously executed cycles along with their results.
func (p *SingleProfilePicker) Pick(
    ctx context.Context,
    cycleState *plugin.CycleState,
    request *requesthandling.InferenceRequest,
    profiles map[string]*requesthandling.Profile,
) (*requesthandling.Profile, error) {
    if len(profiles) != 1 {
        return nil, fmt.Errorf("failed to select a single profile from %d profiles", len(profiles))
    }

    var result *requesthandling.Profile
    for _, profile := range profiles {
        result = profile
        break // assumes a single profile
    }

    return result, nil
}
```

**`ResponseProcessor`** — called during the profile's response stage before sending the response to the user:

The plugin can mutates the response in place via the same `InferenceMessage` helpers as `RequestProcessor`. Runs
after the model server replies. Following is an example of adding a header to the response from the request cycle state.

```go
func (p *ModelNameToHeaderPlugin) ProcessResponse(ctx context.Context, cycleState *plugin.CycleState, response *requesthandling.InferenceResponse) error {
	selectedModel, err := plugin.ReadCycleStateKey[string](cycleState, modelselector.SelectedModelCycleStateKey)
	if err != nil {
		log.FromContext(ctx).V(logutil.VERBOSE).Info("no selected model in CycleState, skipping")
		return nil
	}
	response.SetHeader(bodyfieldtoheader.ModelHeader, selectedModel)
	return nil
}
```
If any plugin in a profile implements this interface, the framework buffers the entire response before calling ProcessResponse.

### Model-selector interfaces ([`modelselector`][modelselector-src])

`Filter` returns the subset of candidates that can serve the request; an empty result is an error.
`Score` returns a score per candidate in `[0, 1]` (values are clamped); multiple scorers combine via
per-reference `weight`. `Pick` selects exactly one model from the scored candidates.

**`Filter`** — receives the full candidate list and returns only the models eligible to serve the
request. The example below is a pass-through that accepts all candidates:

```go
// Filter returns the subset of models that can serve the request.
func (f *MyFilter) Filter(
	   _ context.Context,
	   _ *plugin.CycleState,
	   _ *requesthandling.InferenceRequest,
	   models []datalayer.Model,
) []datalayer.Model {
	   return models
}
```

**`Score`** — returns a score in `[0, 1]` for each candidate. The example is extracted from
[`inflight-requests-scorer`][inflightrequests-scorer-src], which ranks models by their active
request count — the least-loaded model scores 1.0 and the most-loaded scores 0.0:

```go
// Score returns a score in [0,1] for each model based on its in-flight request count.
// Formula: score = (max - count) / (max - min)
func (s *InflightRequestsScorer) Score(
    _ context.Context,
    _ *plugin.CycleState,
    _ *requesthandling.InferenceRequest,
    models []datalayer.Model,
) map[datalayer.Model]float64 {
    var minCount int64 = math.MaxInt64
    var maxCount int64 = math.MinInt64

    requestCounts := make(map[datalayer.Model]int64, len(models))
    for _, model := range models {
        count := inflightRequestCount(model)
        requestCounts[model] = count
        if count < minCount {
            minCount = count
        }
        if count > maxCount {
            maxCount = count
        }
    }

    scores := make(map[datalayer.Model]float64, len(models))
    for _, model := range models {
        if maxCount == minCount {
            scores[model] = 1.0
        } else {
            scores[model] = float64(maxCount-requestCounts[model]) / float64(maxCount-minCount)
        }
    }
    return scores
}
```

**`Pick`** — selects exactly one model from the scored candidates. The example is extracted from
[`max-score-picker`][maxscore-picker-src], which returns the model with the highest aggregate score:

```go
// Pick selects the model with the highest score.
func (p *MaxScorePicker) Pick(
	   ctx context.Context,
	   _ *plugin.CycleState,
	   scoredModels []*modelselector.ScoredModel,
) *modelselector.PipelineRunResult {
	   // Shuffle for random tie-breaking when scores are equal.
	   picker.ShuffleScoredModels(scoredModels)

	   slices.SortStableFunc(scoredModels, func(i, j *modelselector.ScoredModel) int {
	       if i.Score > j.Score {
	           return -1
	       }
	       if i.Score < j.Score {
	           return 1
	       }
	       return 0
	   })

	   return &modelselector.PipelineRunResult{TargetModel: scoredModels[0].Model}
}
```

### Data-layer interfaces ([`datalayer/datasource`][datalayer-src])

**`Extractor`** — called once per event batch, which includes one or more event; must filter internally to the event types it cares
about. The example is from [`request-metadata-extractor`][requestmetadata-src], which increments and
decrements per-model in-flight counters on request/response events:

```go
func (e *RequestMetadataExtractor) Extract(_ context.Context, events []dlsrc.Event) error {
    updated := map[string]RequestMetadataCount{}
    for _, ev := range events {
        switch ev.Type {
        case dlsrc.RequestEventType:
            p, ok := ev.Payload.(dlsrc.RequestPayload)
            if !ok {
                continue
            }
            model, _ := p.Request.Body["model"].(string)
            if model == "" {
                continue
            }
            maxTokens, _ := p.Request.Body["max_tokens"].(float64)
            c := e.counters[model]
            c.Requests++
            c.Tokens += int64(maxTokens)
            e.counters[model] = c
            updated[model] = c
        case dlsrc.ResponseEventType:
            p, ok := ev.Payload.(dlsrc.ResponsePayload)
            if !ok {
                continue
            }
            model, _ := p.Request.Body["model"].(string)
            if model == "" {
                continue
            }
            maxTokens, _ := p.Request.Body["max_tokens"].(float64)
            c := e.counters[model]
            floorDecrement(&c.Requests, 1)
            floorDecrement(&c.Tokens, int64(maxTokens))
            e.counters[model] = c
            updated[model] = c
        }
    }
    for model, c := range updated {
        e.ds.GetOrCreateModel(model).GetAttributes().Put(RequestMetadataAttributeKey, c)
    }
    return nil
}
```

**`Collector`** — `Poll` is called on a timer at the interval returned by `CollectorFrequency`.
The skeleton below matches the interface:

```go
func (c *MyCollector) Poll(_ context.Context) (any, error) { 
    return nil, nil 
}
func (c *MyCollector) CollectorFrequency() time.Duration   {
    return 30 * time.Second
}
```

**`DataSource`** — manages its own watch or control loop. `Start` runs until the context is
cancelled; `Stop` unblocks it and releases resources. The example is from
[`model-config-datasource`][modelconfigcollector-src], which watches a JSON file and keeps the
datastore in sync:

```go
// Start performs an initial sync then watches the config file's parent directory for changes.
func (c *ModelConfigDataSource) Start(ctx context.Context) error {
    if err := c.syncModels(ctx); err != nil {
        return err
    }
    watcher, err := fsnotify.NewWatcher()
    if err != nil {
        return err
    }
    if err := watcher.Add(filepath.Dir(c.absModelsPath)); err != nil {
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
                if absEvent, _ := filepath.Abs(event.Name); absEvent != c.absModelsPath {
                    continue
                }
                if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
                    c.syncModels(ctx) //nolint:errcheck
                }
            }
        }
    }()
    return nil
}

// Stop signals the watcher goroutine to exit and blocks until it has stopped.
func (c *ModelConfigDataSource) Stop() {
    close(c.stopCh)
    <-c.doneCh
}
```

### Implementing a multi-plugin feature

When implementing a multi plug-in feature, the loader creates the
instance **once** from the factory and wires the same object at every matching location in the
pipeline or data layer — there is no second construction. A plugin that implements both
`RequestProcessor` and `Extractor`, for example, is registered once under `plugins`, referenced from
the profile's `request` list, and also from `datalayer.extractors`; the loader recognises both roles
and routes accordingly. Because it is one object, state accumulated in `ProcessRequest` is directly
accessible in `Extract` without any external coordination.


## Testing

Each in-tree plugin ships a unit test next to its source — use them as templates:

- [`body_field_to_header_test.go`][body-field-to-header-test-src] — constructs the plugin, calls
  `ProcessRequest` on a hand-built `InferenceRequest`, and asserts the header mutations (including the
  absent and empty no-op paths).
- [`plugin_test.go`][costaware-test-src] — asserts the `cost-scorer` score map for various price
  distributions.

Tests call the factory or constructor directly and read mutations back through the message helpers
(`MutatedHeaders()`, `BodyMutated()`, ...).

## References

- [Architecture][Architecture] — the IPP pipeline, profiles, model selection, and data layer.
- [Configuration][Configuration] — the full `PayloadProcessorConfig` schema.
- [Plugins][Plugins] — the in-tree plugin reference and configuration model.
- [`plugin.Plugin`][plugin-src] / [registry][registry-src] — the base contract and registration.
- [`requesthandling`][requesthandling-src] / [`modelselector`][modelselector-src] interfaces.

[Architecture]: architecture.md
[Configuration]: configuration.md
[Plugins]: plugins.md
[`single-profile-picker`]: plugins.md#profile-picker-plugins
[plugin-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/interface/plugin/plugins.go
[registry-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/interface/plugin/registry.go
[handle-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/interface/plugin/handle.go
[cycle-state-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/interface/plugin/cycle_state.go
[requesthandling-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/interface/requesthandling/plugins.go
[requesthandling-types-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/interface/requesthandling/types.go
[modelselector-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/interface/modelselector/plugins.go
[datalayer-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/interface/datalayer/datasource/types.go
[single-profile-picker-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/plugins/requesthandling/profilepicker/single/single_profile_picker.go
[body-field-to-header-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/plugins/requesthandling/bodyfieldtoheader/body_field_to_header.go
[body-field-to-header-test-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/plugins/requesthandling/bodyfieldtoheader/body_field_to_header_test.go
[costaware-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/plugins/modelselector/scorer/costaware/plugin.go
[inflightrequests-scorer-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/plugins/modelselector/scorer/inflightrequests/plugin.go
[maxscore-picker-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/plugins/modelselector/picker/maxscore/picker.go
[costaware-test-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/plugins/modelselector/scorer/costaware/plugin_test.go
[runner-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/cmd/runner/runner.go
