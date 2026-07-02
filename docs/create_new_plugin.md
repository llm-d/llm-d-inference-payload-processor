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

A plugin then **additionally implements** one extension-point interface; the loader inspects which it
satisfies and routes it to the matching pipeline stage:

| Interface | Package | Role |
|-----------|---------|------|
| `PreProcessor` / `PostProcessor` | [`requesthandling`][requesthandling-src] | Reserved global stages (before profile selection / after the response plugins). Defined in the API but not yet invoked by the request path. |
| `RequestProcessor` | [`requesthandling`][requesthandling-src] | Inspect and mutate the request before routing. |
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

## Other extension points

A model-selector plugin has the same shape but implements one of the
[`modelselector`][modelselector-src] interfaces (`Filter`, `Scorer`, or `Picker`) instead of
`RequestProcessor`. For example, a `Scorer` implements `Score(...) map[datalayer.Model]float64`,
returning a score per candidate. The [`cost-scorer`][costaware-src] is a concrete reference; its
factory and `WithName` follow this tutorial (it ships in-tree but, like `model-config-datasource`, is
not registered in the default runner — add its factory to `registerInTreePlugins` to use it). In
configuration, a Scorer is referenced from a profile's `request` list alongside the `model-selector`
entry and **requires** a `weight`. See [Plugins][Plugins] for the full Filter/Scorer/Picker set.

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
[body-field-to-header-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/plugins/requesthandling/bodyfieldtoheader/body_field_to_header.go
[body-field-to-header-test-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/plugins/requesthandling/bodyfieldtoheader/body_field_to_header_test.go
[costaware-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/plugins/modelselector/scorer/costaware/plugin.go
[costaware-test-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/plugins/modelselector/scorer/costaware/plugin_test.go
[runner-src]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/cmd/runner/runner.go
