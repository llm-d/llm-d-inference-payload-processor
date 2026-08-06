# `model-group` Name Filter

Filter the candidate model names based on the request body mode name field.
Support explicit model selection by specifying a model name in the `model` field.
Support using `auto` or `auto/group-name` for selecting more than one model candidate.
`auto` (or no model name field) enables using all candidate models (no filtering), while using the pattern  `auto/group-name` selects
the models that belong to the group `group-name`.

It is registered as type `model-group-name-filter` and runs as a modelselector filter. The filter takes no
plugin parameters: group membership is not configured on the filter itself. Instead it is resolved at filter
time from each candidate model's `modelgroups.GroupsAttributeKey` attribute in the datalayer, which is
populated by the `model-config-datasource` plugin (`pkg/framework/plugins/datalayer/modelconfigcollector`)
from the `groups` section of its config file. This lets group membership be updated dynamically (e.g. via a
Kubernetes ConfigMap) without restarting or reconfiguring this filter.

When the model name is set to `auto/group-name` the filter matches the requested `group-name` against each
candidate model's group attribute and keeps only the candidates that belong to that group.

## What it does

1. Reads the `model` field from the request body.
2. If the field is absent, an empty string or `auto`, all incoming candidates are kept.
3. If the `model` field is formatted as `auto/group-name`, with the prefix `auto` and separator `/`, it extracts `group-name` and keeps the candidate models whose `modelgroups.GroupsAttributeKey` attribute lists that group name.
4. If the `model` field is a non-empty string that is not `auto` and does not start with `auto/`, it is treated as an explicit model name and the single matching candidate is kept.
5. If the intersection is empty or the field is malformed (not a string), the filter returns no candidates and the pipeline rejects the request with HTTP 429.

## Inputs consumed

- The `model` field of the request body.
- The candidate model list passed in by the pipeline, with each model's `modelgroups.GroupsAttributeKey` attribute (if any), as populated by the `model-config-datasource` plugin.

## Example configuration

```yaml
plugins:
- type: model-group-name-filter
```

Group membership itself is configured on the `model-config-datasource` plugin's config file, not here.
That plugin's config file (pointed to by its `modelsPath` parameter) lists models (omitted in the example) and,
alongside them, a `groups` section mapping a group name to the model names that belong to it:

```json
{
  "groups": [
    { "name": "fast", "models": ["qwen3-8b", "gpt-oss-20b"] },
    { "name": "planning", "models": ["gpt-oss-120b", "gemma4"] }
  ]
}
```

With this configuration snippet, a request with `model` set to `auto/fast` is filtered to the candidates `qwen3-8b` and
`gpt-oss-20b`; `auto/planning` is filtered to `gpt-oss-120b` and `gemma4`. A model can appear in more than
one group's `models` list. A group with an empty `name` or an empty `models` list is skipped (and logged) by `model-config-datasource`, as is a group entry naming a model not present in the top-level
`models` list.
