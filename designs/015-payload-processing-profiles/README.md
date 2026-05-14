# Payload Processing Profiles

Author(s): @noyitz

Related issue: [#15](https://github.com/llm-d/llm-d-inference-payload-processor/issues/15)
Related proposals: [Proposal 043 — ModelSelector Architecture](https://github.com/llm-d/llm-d-inference-payload-processor/tree/main/docs/proposals/043-model-selection-framework)

## Summary

This document describes the runtime architecture for introducing profiles and a profile picker to the Inference Payload Processor (IPP). Profiles allow different plugin chains to run for different types of requests — for example, one chain when the model is specified in the request, and a different chain when the model needs to be auto-selected.

This design focuses on the runtime components and their interactions. The configuration format (how profiles are defined in YAML/CRD) is covered separately in [#77](https://github.com/llm-d/llm-d-inference-payload-processor/issues/77).

## Current State

Today the IPP runs a single, fixed chain of plugins for every request:

```
Request → [RequestPlugin₁ → RequestPlugin₂ → ... → RequestPluginₙ] → Dispatch → [ResponsePlugin₁ → ResponsePlugin₂ → ... → ResponsePluginₘ] → Response
```

All requests go through the same plugins in the same order. The order is determined at startup via command-line flags. There is no way to conditionally run different plugins for different requests.

This works for simple use cases (extract model name, resolve provider, inject credentials). But as the IPP takes on intelligent routing responsibilities — model selection, cost-based routing, fallback — different requests need fundamentally different processing paths.

## Motivation

Consider these scenarios:

**Scenario 1: Model specified in request**
The user sends `{"model": "llama-70b"}`. The IPP needs to resolve the provider, translate the API format, and inject credentials. No model selection is needed.

**Scenario 2: Auto-selection mode**
The user sends `{"model": "auto"}`. The IPP needs to run a model selector (Filter → Score → Pick) to choose the best model, then proceed with provider resolution and credential injection.

**Scenario 3: Priority routing**
A high-priority request (indicated by a header) should use a different model selection strategy than a standard request — perhaps a quality-optimized selector instead of a cost-optimized one.

Each scenario requires a different set of plugins in a different order. Profiles make this possible.

## Proposed Architecture

The IPP pipeline is organized into three stages:

### Stage 1: Pre-Processing

A set of shared plugins that always run for every request, regardless of which profile is selected. These plugins enrich the request with information that the profile picker needs to make its decision.

Pre-processing is necessary because the profile picker often cannot decide at the start of the request. For example, the picker may need to know whether the user specified a model or requested auto-selection. But the model name lives in the request body — it needs to be extracted by `body-field-to-header` before the picker can check for it. Without pre-processing, the picker would have to duplicate body-parsing logic, breaking the plugin composability model.

### Stage 2: Profile Selection

A profile picker plugin examines the enriched request and selects which profile to run. The picker is a plugin that implements a defined interface — it receives the request (after pre-processing has run) and the set of available profiles, and returns the name of the selected profile.

How the picker makes its decision is an implementation concern. It could match on headers, path, body fields, CycleState values, or any combination. The framework does not prescribe the decision logic — it only defines when the picker runs and what it receives.

Exactly one profile picker must be configured. If no picker is configured, the system should fall back to a default profile.

### Stage 3: Profile Execution

The selected profile's plugins run in order. A profile defines two ordered chains:

- **Request chain** — request plugins that run before dispatch to the upstream model server
- **Response chain** — response plugins that run after the response returns from the upstream

The request chain may include a model selector as one of its plugins — the model selector is a regular RequestProcessor plugin, not a special concept at the profile level.

## Execution Flow

```
Request arrives
    │
    ▼
Pre-Processing Plugins (shared, always run)
    │  e.g., body-field-to-header, request-guard
    │
    ▼
Profile Picker Plugin
    │  examines enriched request
    │  selects profile by name
    │
    ▼
Selected Profile — Request Chain
    │  e.g., [model-selector → model-provider-resolver → api-translation → apikey-injection]
    │
    ▼
Dispatch to Model Server
    │
    ▼
Selected Profile — Response Chain
    │  e.g., [metrics-collector → response-guard]
    │
    ▼
Response returned
```

## What is a Profile

A profile is a named unit that groups:

- An ordered list of request plugins (RequestProcessors)
- An ordered list of response plugins (ResponseProcessors)

Different profiles can contain different plugins in different order. The same plugin instance can appear in multiple profiles if needed.

A profile does not manage its own lifecycle — it is a configuration construct that the framework uses to determine which plugins to run for a given request.

## What is a Profile Picker

The profile picker is a plugin that implements a dedicated interface. It receives:

- The request (after pre-processing plugins have run)
- CycleState (shared state from pre-processing)
- The set of available profile names

It returns the name of the profile to execute.

The profile picker runs exactly once per request. It does not iterate or re-pick (unlike the upstream scheduler's ProfileHandler which can iteratively select profiles based on previous results). If iterative profile selection is needed in the future, the interface can be extended.

If the picker returns an unknown profile name or an error, the framework returns an error to the client.

## Model Selector Integration

The ModelSelector ([Proposal 043](https://github.com/llm-d/llm-d-inference-payload-processor/tree/main/docs/proposals/043-model-selection-framework)) is a RequestProcessor plugin that internally runs its own Filter → Score → Pick pipeline to select the best model for a request. It is not a special concept at the profile level — it sits in a profile's request chain alongside other request plugins.

### How the ModelSelector works internally

The ModelSelector has its own layered architecture that mirrors the IPP profile structure:

- **ModelSelector** — the top-level component. Receives candidate models and runs a ModelSelectorProfile to select the best one.
- **ModelSelectorProfile** — an ordered pipeline of Filter → Score → Pick phases, analogous to an IPP profile's ordered plugin chain.
- **Filter plugins** — remove models that are unfit for selection (e.g., rate-limited, unavailable). Zero or more per profile.
- **Scorer plugins** — score remaining models in a normalized [0,1] range with configurable weights (e.g., CostScorer, LatencyScorer). Zero or more per profile. When multiple scorers are configured, each scorer's output is multiplied by its weight and accumulated into a single weighted score per model.
- **Picker plugin** — selects the winning model based on the weighted scores produced by the scorers. The picker receives the full list of scored models and uses those scores to make its decision. For example, MaxScorePicker selects the model with the highest weighted score, while WeightedRandomPicker uses the scores as probabilities — a model scored 0.9 is much more likely to be selected than one scored 0.1. Exactly one per profile.

The data flows through the pipeline sequentially: filters narrow the candidate set, scorers evaluate and rank the remaining candidates, and the picker makes the final selection using those rankings.

The ModelSelector receives a CycleState from the IPP pipeline, allowing it to share data with other plugins in the same profile (e.g., reading model metadata written by a pre-processing plugin).

### How it fits in the IPP profile

The ModelSelector appears as a single step in a profile's request chain. From the IPP profile's perspective, it is just another RequestProcessor — the internal Filter/Score/Pick pipeline is transparent:

```
Profile: auto-routing
  Request Chain:
    1. model-selector (internally runs: Filter → Score → Pick → writes selected model)
    2. model-provider-resolver (reads selected model, resolves provider)
    3. api-translation (translates to provider format)
    4. apikey-injection (injects provider credentials)
```

### Multiple model selectors across profiles

Different profiles can use different ModelSelector instances, each configured with different Filter/Scorer/Picker plugins:

- A **standard** profile might not include a ModelSelector at all — the user specified the model directly
- An **auto-routing** profile might include a cost-optimized ModelSelector (CostScorer + MaxScorePicker)
- A **priority** profile might include a quality-optimized ModelSelector (QualityScorer + MaxScorePicker)

This is one of the key benefits of treating the ModelSelector as a regular plugin — it composes naturally with the profile system without any special-casing.

### Recursive pattern

The ModelSelector can itself support multiple internal profiles with its own profile selection — the same pattern as the IPP profile system, applied recursively. For example, a single ModelSelector plugin could internally select between a cost-optimized scoring profile and a quality-optimized scoring profile based on request properties. This is internal to the ModelSelector and transparent to the IPP profile that contains it.

This recursive pattern — profiles containing plugins that themselves contain profiles — is a deliberate design choice. It means we design the profile/picker pattern once and reuse it at both the IPP level and the ModelSelector level.

### Current implementation status

The ModelSelector framework is implemented and merged:

- **ModelSelector.Select()** — the entry point that runs a single profile ([PR #72](https://github.com/llm-d/llm-d-inference-payload-processor/pull/72), merged)
- **ModelSelectorProfile.Run()** — executes the Filter → Score → Pick pipeline with per-plugin latency metrics
- **WeightedScorer** — wraps scorers with configurable weights for score aggregation
- **MaxScorePicker** — picks the highest-scored model with random tie-breaking ([PR #74](https://github.com/llm-d/llm-d-inference-payload-processor/pull/74))
- **WeightedRandomPicker** — selects proportionally to scores using the A-Res sampling algorithm ([PR #74](https://github.com/llm-d/llm-d-inference-payload-processor/pull/74))

Filter and scorer plugin implementations (cost, latency, rate-limit) are planned as separate issues.

## Relationship to Config API

This design describes the runtime architecture — what components exist, how they connect, and in what order they execute. How these components are configured (YAML format, plugin references, parameter passing) is the responsibility of [#77](https://github.com/llm-d/llm-d-inference-payload-processor/issues/77).

This design should be implementable regardless of the config format chosen. The key requirements for the config are:

- Ability to define a set of pre-processing plugins with their order
- Ability to define multiple named profiles, each with an ordered request chain and response chain
- Ability to specify which plugin serves as the profile picker
- Ability to define a default profile

## Inspiration from Upstream Scheduler

The upstream llm-d scheduler uses a `ProfileHandler` with two methods:

- `Pick()` — selects which profiles to run, called iteratively until no more profiles are returned
- `ProcessResults()` — aggregates results from all profile runs and designates a primary result

Our profile picker is simpler:

- It picks once (no iterative re-picking)
- There is no result aggregation — only one profile runs per request
- The response chain of the selected profile handles any post-processing

The scheduler's iterative pattern could be added later if a use case emerges, but for the initial implementation a single pick is sufficient.

## Open Questions

1. **Should response plugins also be profile-specific, or always shared?** The current design makes them profile-specific. An alternative is to have shared post-processing (like shared pre-processing) that always runs regardless of profile. This could be useful for metrics collection that should happen for every response.

2. **Pre-processing on the response side?** Similar to request pre-processing, there may be shared response plugins that should always run (e.g., metrics collection, logging). Should we support a shared response post-processing stage?

3. **Error handling in the profile picker** — If the picker cannot determine a profile (ambiguous request, missing data), should it fall back to a default profile, or return an error? The design currently says error, but a default fallback may be more resilient.

4. **Profile inheritance** — Should a profile be able to extend another profile (e.g., "auto-routing is the same as standard but with model-selector added before model-provider-resolver")? This reduces duplication but adds complexity. Can be deferred.
