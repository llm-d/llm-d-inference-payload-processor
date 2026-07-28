# IPP Plugins Reference

## Table of Contents

- [Overview](#overview)
- [Plugin Categories](#plugin-categories)
- [Processing Pipeline](#processing-pipeline)
- [Request Handling Plugins](#request-handling-plugins)
  - [`body-field-to-header`](#body-field-to-header)
  - [`base-model-to-header`](#base-model-to-header)
  - [`model-selector`](#model-selector)
- [Profile Picker Plugins](#profile-picker-plugins)
  - [`single-profile-picker`](#single-profile-picker)
- [Model Selector Plugins](#model-selector-plugins)
  - [Filters](#filters)
    - [`model-group-name-filter`](#model-group-name-filter)
  - [Scorers](#scorers)
    - [`cost-scorer`](#cost-scorer)
    - [`inflight-requests-scorer`](#inflight-requests-scorer)
  - [Pickers](#pickers)
    - [`max-score-picker`](#max-score-picker)
    - [`random-picker`](#random-picker)
    - [`weighted-random-picker`](#weighted-random-picker)
- [Data Layer Plugins](#data-layer-plugins)
  - [Collectors](#collectors-1)
  - [Extractors](#extractors)
    - [`request-metadata-extractor`](#request-metadata-extractor)
  - [Datasources](#datasources)
    - [`model-config-datasource`](#model-config-datasource)
- [Response Handling Plugins](#response-handling-plugins)
  - [`model-name-to-header`](#model-name-to-header)
- [Pre- and Post-Processors](#pre--and-post-processors)
- [Configuration Example](#configuration-example)
- [References](#references)

---

## Overview

All IPP behavior is implemented as **plugins**. The framework defines the pipeline and the extension
points; concrete behavior lives in plugins that are selected, parameterized, and ordered in a
[`PayloadProcessorConfig`][Configuration]. Each plugin has a **registered type name** (a constant in
its source) and a configurable instance **name**; because instances are named, the same plugin type
can be configured more than once with different parameters.

This document lists every in-tree plugin, grouped by category, with its registered type name,
purpose, parameters, and a link to its source. For the conceptual model — profiles, the ext-proc
lifecycle, model selection, and the data layer — see the [Architecture][Architecture] document.

---

## Plugin Categories

A plugin belongs to exactly one category, determined by the framework interface it implements. The
config loader routes each plugin to the right extension point based on that interface.

| Category | Purpose | When executed |
|----------|---------|---------------|
| **Request Handling** | Inspect and mutate the request (headers, body) before it is routed. | During a profile's `request` stage, before the model server is reached. |
| **Response Handling** | Inspect and mutate the response on its way back to the client. | During a profile's `response` stage, after the model server replies. |
| **Model Selector — Filter** | Remove candidate models that cannot serve the request. | First phase of the ModelSelector pipeline (inside `model-selector`). |
| **Model Selector — Scorer** | Score the remaining candidate models, conventionally in `[0, 1]`. | Second phase of the ModelSelector pipeline; scores combine via per-reference `weight`. |
| **Model Selector — Picker** | Select exactly one final model from the scored candidates. | Third phase of the ModelSelector pipeline; exactly one picker runs. |
| **Profile Picker** | Choose which profile runs for a request. | Globally, before the profile's request plugins. |
| **Data Layer — Collector** | Aggregate cross-request signals over time and write them into the datastore for other plugins to consume. | Runs continuously in the background on a timer, independent of any single request. |
| **Data Layer — Extractor** | Derive cross-request metadata from request/response events and persist it in the datastore. | Runs in the background when request/response events arrive, independent of any single request profile stage. |
| **Data Layer — Datasource** | Import external configuration or metadata into the datastore and keep it in sync as the source changes. | Starts once in the background, performs an initial sync, then keeps watching its source for updates. |

---

## Processing Pipeline

IPP executes plugins in a fixed sequence of stages:

```
ProfilePicker → Profile Request Plugins → [Model Server] → Profile Response Plugins
```

(The config API also defines global `preProcessing` / `postProcessing` stages; these are reserved
extension points and are not yet invoked by the request path — see [Architecture][Architecture].)

Plugins are **declared once** under the top-level `plugins` list of the `PayloadProcessorConfig`
(each with a `type`, an optional `name`, and optional `parameters`) and then **referenced by name**
elsewhere in the config via `pluginRef`:

- `profiles[].plugins.request[]` and `profiles[].plugins.response[]` reference request- and
  response-handling plugins. A profile's `request` list may also reference model-selector plugins
  (Filter / Scorer / Picker); the loader routes each reference by the interface the plugin
  implements. Scorer references carry a `weight`.
- `profilePicker.pluginRef` references the profile picker. When exactly one profile is defined and no
  picker is configured, [`single-profile-picker`](#single-profile-picker) is enabled automatically.
- `preProcessing.plugins[]` and `postProcessing.plugins[]` reference pre- and post-processors.
- `datalayer.collectors[]`, `datalayer.extractors[]`, and `datalayer.datasources[]` reference
  data-layer plugins. These are **not** part of any profile's request list.

For the conceptual model behind profiles, model selection, and the data layer, see
[Architecture][Architecture]. For the full configuration schema, see [Configuration][Configuration].

---

## Request Handling Plugins

Request-handling plugins implement the `RequestProcessor` interface and process the request body and
headers before routing.

### `body-field-to-header`

Extracts a single field from the JSON request body and sets its value as an HTTP header. If the
field is absent or empty, the plugin records a metric and skips without error. This is the generic
building block behind model-aware routing — for example, copying the `model` body field into the
`X-Gateway-Model-Name` header.

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `fieldName` | string | yes | Name of the request-body field to extract. |
| `headerName` | string | yes | Name of the HTTP header to set with the extracted value. |

**Source:** [`pkg/framework/plugins/requesthandling/bodyfieldtoheader/`][src-bodyfieldtoheader]

### `base-model-to-header`

Maps the request's `model` name — including LoRA adapter names — to its **base model** and writes the
result to the `X-Gateway-Base-Model-Name` routing header. The adapter-to-base-model mapping is
maintained by a ConfigMap reconciler that watches IPP-managed model-mapping ConfigMaps, so the
mapping updates at runtime without restarts. If the request has no `model` field the plugin skips; if
the model is neither a known adapter nor a registered base model, the header is set to the empty
string. This plugin powers [multi-pool routing][Architecture].

**Parameters:** None. The plugin is wired to the controller-runtime client and reconciler via its
plugin handle; the model mappings come from labeled ConfigMaps (see [Configuration][Configuration]),
not from plugin parameters.

**Source:** [`pkg/framework/plugins/requesthandling/basemodelextractor/`][src-basemodelextractor]

### `model-selector`

Entry point for the **ModelSelector** framework. When present in a profile's `request` list, it runs
the `Filter → Score → Pick` pipeline over the candidate models in the datastore, then writes the
selected model name back into the request body's `model` field so the rest of the pipeline proceeds
as if the client had requested that model directly. The Filter, Scorer, and Picker plugins for this
profile are declared in the same profile `request` list and wired into the selector by the config
loader; if no picker is referenced, [`max-score-picker`](#max-score-picker) is used by default. If no
candidate models are available, the plugin returns an error.

**Parameters:** None. The pipeline is assembled from the profile's other model-selector references,
not from parameters on this plugin.

**Source:** [`pkg/framework/plugins/requesthandling/modelselector/`][src-modelselector]

---

## Profile Picker Plugins

A profile picker chooses which profile runs for a request. It implements the `ProfilePicker`
interface and is referenced via the top-level `profilePicker` field.

### `single-profile-picker`

Selects the single configured profile for every request. It is **enabled by default** when exactly
one profile is defined and no profile picker is configured, so you typically do not declare it
explicitly. Useful when an IPP deployment runs a single processing path.

**Parameters:** None.

**Source:** [README][readme-single]

---

## Model Selector Plugins

Model-selector plugins implement the ModelSelector framework's `Filter`, `Scorer`, and `Picker`
interfaces. They are referenced inside a profile's `request` list alongside the
[`model-selector`](#model-selector) plugin, and the loader routes each reference to the correct phase
by interface. See the [ModelSelector proposal][ModelSelector proposal] for the framework design.

### Filters

Filters remove candidate models that cannot serve a request; if filtering leaves zero candidates the
framework returns an error to the client.

#### `model-group-name-filter`

Restricts the candidate models based on the request body's `model` field. Supports explicit model
selection by name, `auto` (or an absent/empty field) to keep all candidates, and `auto/<group-name>`
to select the models belonging to a named group.

- Absent, empty, or exactly `auto`: all incoming candidates are kept.
- `auto/<group-name>`: each candidate model is checked for `<group-name>` in its own
  `modelgroups.GroupsAttributeKey` attribute; only candidates that list that group are kept.
- Any other non-empty string: the single candidate whose name matches that string is kept.
- An unknown group, an empty intersection, or a malformed (non-string) `model` field: no candidates
  are returned, and the pipeline rejects the request with HTTP 429.

**Parameters:** None.

```yaml
plugins:
- type: model-group-name-filter
```

**Source:** [`pkg/framework/plugins/modelselector/filter/modelgroup/`][src-modelgroupfilter]

To add additional filters, implement the `Filter` interface and register it (see
[Creating a Plugin][Creating a Plugin]).

### Scorers

Scorers assign each remaining candidate model a score, conventionally in `[0, 1]`. Multiple scorers
combine via the per-reference `weight` set in the profile (a scorer reference **requires** a
`weight`).

#### `cost-scorer`

Scores candidate models by price so that cheaper models score higher. Each model carries a `price`
attribute (USD per 1M tokens); the score is computed with inverted sum normalization,
`1 - price / sum(prices)`. A single candidate receives a neutral score of `0.5`, and when every
candidate's price is zero all candidates receive `1.0`. Use it for cost-aware model selection.

**Parameters:** None. Prices are read from each model's `price` attribute in the datastore, not from
plugin parameters.

**Source:** [`pkg/framework/plugins/modelselector/scorer/costaware/`][src-costscorer]

> [!NOTE]
> `cost-scorer` ships in-tree as a reference implementation but is **not registered in the default
> runner**. To use it, register its factory in `registerInTreePlugins` (see
> [Creating a Plugin][Creating a Plugin]) or register it from your own runner build.

#### `inflight-requests-scorer`

Scores candidate models by current in-flight request load so that the least-loaded model scores
highest. With per-model in-flight counts `count`, the score is `(max - count) / (max - min)`; the
least-loaded model scores `1.0` and the most-loaded scores `0.0`. Models with no in-flight-request
attribute are treated as idle (zero), and if all candidates share the same count they all score
`1.0`. It consumes the in-flight counts produced by
[`request-metadata-extractor`](#request-metadata-extractor), so configure that data-layer extractor
alongside this scorer.

**Parameters:** None. In-flight counts are read from each model's `request-metadata` attribute in the
datastore.

**Source:** [`pkg/framework/plugins/modelselector/scorer/inflightrequests/`][src-inflightscorer]

### Pickers

A picker selects the single final model from the scored candidates. Exactly one picker runs per
model-selection profile; if none is referenced, [`max-score-picker`](#max-score-picker) is added by
default.

#### `max-score-picker`

Selects the candidate with the highest score, shuffling first so that ties are broken at random. It
maximizes adherence to the scoring objective (e.g. lowest cost or lowest load) but is susceptible to
**hot-spotting** when many concurrent requests produce identical scores for the same model.

**Parameters:** None.

**Source:** [README][readme-maxscore]

#### `random-picker`

Selects a candidate uniformly at random, ignoring all scores. It gives a strictly uniform load
distribution — immune to hot-spotting, but unable to leverage cost or load signals.

**Parameters:** None.

**Source:** [README][readme-random]

#### `weighted-random-picker`

Selects a candidate at random with probability proportional to its score, using the **A-Res**
(Algorithm for Reservoir Sampling) algorithm for mathematically correct weighted sampling. It
balances the trade-off between `max-score-picker` and `random-picker`, favoring higher-scoring models
while retaining exploration to avoid extreme hot-spotting. If every candidate scores zero or less, it
falls back to `random-picker` for uniform selection.

**Parameters:** None.

**Source:** [README][readme-weightedrandom]

---

## Data Layer Plugins

Data-layer plugins maintain cross-request state consumed by Scorers, Filters, and any other plugin
that needs shared runtime data. As described in [Architecture][Architecture], the data layer is split
into three plugin categories:

- **Collectors** aggregate signals over time on a timer.
- **Extractors** process request/response events as they arrive.
- **Datasources** import external state and keep the datastore synchronized with it.

These plugins are referenced only from the top-level `datalayer` section as `collectors`,
`extractors`, or `datasources` — **never** from a profile's `request` list. They run outside the
per-request pipeline, in background loops owned by the data layer.

### Collectors

Collectors aggregate signals over time and write the results into the shared datastore. They are
started by the data layer and run continuously on their own schedule, independent of request flow.

### Extractors

Extractors consume request and response events emitted by the data layer's event stream. They do not
run as ordinary profile plugins; instead, the data layer invokes them in the background whenever new
events arrive.

#### `request-metadata-extractor`

An **extractor** that tracks in-flight request counts and token sums per model. On each request event
it increments the model's request count and adds the request's `max_tokens` to its token sum; on the
corresponding response event it decrements both (flooring at zero). The extractor batches the changed
models for that event set and writes the result to each model's `request-metadata` attribute, which
[`inflight-requests-scorer`](#inflight-requests-scorer) consumes. Reference it under
`datalayer.extractors`.

**How it runs:** The data layer calls [`RequestMetadataExtractor.Extract()`](pkg/framework/plugins/datalayer/requestmetadata/plugin.go:83) whenever request/response events arrive. The extractor updates its in-memory counters and persists the latest values to the shared datastore.

**Parameters:** None. The extractor is wired to the shared datastore via its plugin handle.

**Source:** [`pkg/framework/plugins/datalayer/requestmetadata/`][src-requestmetadata]

### Datasources

Datasources import state from an external source into the shared datastore. The data layer starts them
in the background; a datasource typically performs an initial sync and then keeps watching its source
for changes.

#### `model-config-datasource`

A **datasource** that imports the set of known model names into the datastore from a JSON config
file, keeping the datastore in sync as the file changes. It watches the file's parent directory (to
handle atomic, rename-based replacements such as Kubernetes ConfigMap remounts), registers every
listed model, and removes any datastore model no longer present in the file. This populates the
candidate-model set that [`model-selector`](#model-selector) reads. Reference it under
`datalayer.datasources`.

**How it runs:** When the data layer starts the plugin, [`ModelConfigDataSource.Start()`](pkg/framework/plugins/datalayer/modelconfigcollector/plugin.go:106) performs an initial sync from `modelsPath`, then launches a background filesystem watcher on the parent directory. On matching file `Write` or `Create` events it re-reads the config and reconciles the datastore.

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `modelsPath` | string | yes | Path to a JSON file with the schema `{"models": [{"name": "..."}]}`. Must be an existing file (not a directory). |

**Source:** [`pkg/framework/plugins/datalayer/modelconfigcollector/`][src-modelconfig]

> [!NOTE]
> `model-config-datasource` ships in-tree as a reference implementation but is **not registered in the
> default runner**. To use it, register its factory in `registerInTreePlugins` (see
> [Creating a Plugin][Creating a Plugin]) or register it from your own runner build.

---

## Response Handling Plugins

Response-handling plugins process the response on its way back to the client. There are three
plugin interfaces:

| Interface | When executed | Body access |
|-----------|-------------|-------------|
| `ResponseHeadersProcessor` | During the response-headers phase, before the body arrives. Works for both streaming and buffered responses. | No |
| `ResponseProcessor` | After the full response body is buffered and parsed as JSON. Forces response buffering. | Yes |
| `ResponseChunkProcessor` | Per-chunk as the response streams through. Cannot be mixed with `ResponseProcessor` in the same profile. | Chunk only |

### `model-name-to-header`

Echoes the model name that was selected during request processing back to the client as a response
header. When [`model-selector`](#model-selector) runs, it resolves the client's request to a specific
model and stores the result in `CycleState`; this plugin reads that stored value during the
response-headers phase and sets the header so that clients can discover which model actually served
their request. Because it runs at the response-headers phase (implementing `ResponseHeadersProcessor`),
it works for both streaming and non-streaming responses. This is useful when the client sends a logical
model name (e.g. a model group or alias) and needs to know the concrete model that was selected — for
example, to pin follow-up requests to the same model for session affinity.

**Prerequisites:** The [`model-selector`](#model-selector) plugin must be configured in the same
profile's `request` list. If no model selection occurred (the `CycleState` key is absent), the
plugin silently skips without error.

**Parameters:**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `headerName` | string | no | `X-Gateway-Model-Name` | Name of the response header to set. |

**Source:** [`pkg/framework/plugins/responsehandling/modelnametoheader/`][src-modelnametoheader]

---

## Pre- and Post-Processors

**PreProcessing** runs before profile selection and is available as a global extension point.
**PostProcessing** runs after the per-profile response plugins and applies to all profiles.

Post-processing plugins are classified by their interface:

| Interface | Phase | Forces buffering |
|-----------|-------|-----------------|
| `ResponseHeadersProcessor` | Response headers (before body) | No |
| `ResponseProcessor` | Response body (after JSON unmarshal) | Yes |

`model-name-to-header` is an in-tree post-processor. Because it implements
`ResponseHeadersProcessor`, it runs in the response-headers phase and works for both streaming and
non-streaming responses without forcing response buffering.

---

## Configuration Example

A complete `PayloadProcessorConfig` that performs cost- and load-aware model selection. Plugins are
declared once under the top-level `plugins` list and referenced by name. The model-selection profile
references `model-selector` together with two weighted scorers and the `weighted-random-picker`, plus
the two header plugins. The `request-metadata-extractor` and `model-config-datasource` are wired under
the top-level `datalayer` section — not in the profile's request list.

> [!NOTE]
> This example uses `cost-scorer` and `model-config-datasource`, which are in-tree but not registered
> in the default runner (see their notes above). Register their factories before applying this config,
> or drop them to run with the default plugin set.

```yaml
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
plugins:
- type: body-field-to-header
  parameters:
    fieldName: model
    headerName: X-Gateway-Model-Name
- type: base-model-to-header
- type: model-selector
- type: cost-scorer
- type: inflight-requests-scorer
- type: weighted-random-picker
- type: request-metadata-extractor
- type: model-name-to-header
- type: model-config-datasource
  parameters:
    modelsPath: /etc/ipp/models.json
profiles:
- name: model-selection
  plugins:
    request:
    - pluginRef: body-field-to-header
    - pluginRef: base-model-to-header
    - pluginRef: model-selector
    - pluginRef: cost-scorer
      weight: 1.0
    - pluginRef: inflight-requests-scorer
      weight: 2.0
    - pluginRef: weighted-random-picker
postProcessing:
- pluginRef: model-name-to-header
datalayer:
  extractors:
  - pluginRef: request-metadata-extractor
  datasources:
  - pluginRef: model-config-datasource
```

With a single profile and no `profilePicker` configured, IPP auto-enables
[`single-profile-picker`](#single-profile-picker). The `model-config-datasource` populates the
candidate models, `request-metadata-extractor` maintains their in-flight counts, and the two scorers
combine by weight (here the load signal is weighted twice the cost signal) before
`weighted-random-picker` chooses the final model.

For the full schema, Helm values, ConfigMaps, and proxy integration, see [Configuration][Configuration].

---

## References

- [Architecture][Architecture] — The conceptual model: ext-proc, profiles, model selection, and the data layer.
- [Configuration][Configuration] — Full configuration reference for the `PayloadProcessorConfig` API.
- [Creating a Plugin][Creating a Plugin] — Tutorial for writing and registering a custom plugin.
- [ModelSelector proposal][ModelSelector proposal] — Design of the model-selection framework.

[Architecture]: architecture.md
[Configuration]: configuration.md
[Creating a Plugin]: create_new_plugin.md
[ModelSelector proposal]: proposals/043-model-selection-framework/README.md

[src-bodyfieldtoheader]: https://github.com/llm-d/llm-d-inference-payload-processor/tree/main/pkg/framework/plugins/requesthandling/bodyfieldtoheader
[src-basemodelextractor]: https://github.com/llm-d/llm-d-inference-payload-processor/tree/main/pkg/framework/plugins/requesthandling/basemodelextractor
[src-modelselector]: https://github.com/llm-d/llm-d-inference-payload-processor/tree/main/pkg/framework/plugins/requesthandling/modelselector
[src-modelgroupfilter]: https://github.com/llm-d/llm-d-inference-payload-processor/tree/main/pkg/framework/plugins/modelselector/filter/modelgroup
[src-costscorer]: https://github.com/llm-d/llm-d-inference-payload-processor/tree/main/pkg/framework/plugins/modelselector/scorer/costaware
[src-inflightscorer]: https://github.com/llm-d/llm-d-inference-payload-processor/tree/main/pkg/framework/plugins/modelselector/scorer/inflightrequests
[src-requestmetadata]: https://github.com/llm-d/llm-d-inference-payload-processor/tree/main/pkg/framework/plugins/datalayer/requestmetadata
[src-modelconfig]: https://github.com/llm-d/llm-d-inference-payload-processor/tree/main/pkg/framework/plugins/datalayer/modelconfigcollector
[src-modelnametoheader]: https://github.com/llm-d/llm-d-inference-payload-processor/tree/main/pkg/framework/plugins/responsehandling/modelnametoheader
[readme-single]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/plugins/requesthandling/profilepicker/single/README.md
[readme-maxscore]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/plugins/modelselector/picker/maxscore/README.md
[readme-random]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/plugins/modelselector/picker/random/README.md
[readme-weightedrandom]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/framework/plugins/modelselector/picker/weightedrandom/README.md
