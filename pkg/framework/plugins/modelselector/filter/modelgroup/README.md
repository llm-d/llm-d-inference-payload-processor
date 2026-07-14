# `model-group` Name Filter

Filter the candidate model names based on the request body mode name field.
Support explicit model selection by specifying a model name in the `model` field.
Support using `auto` or `auto/group-name` for selecting more than one model candidate.
`auto` (or no model name field) enables using all candidate models (no filtering), while using the pattern  `auto/group-name` selects
the models that are listed in the group `group-name` that is provided in the configuration.
The filter is configured with a list of groups, each group has a name, and a list of models for the group.

It is registered as type `model-group-name-filter` and runs as a modelselector filter.

When the model name is set to `auto/group-name` the filter matches the requested `group-name` against the list of groups defined in the filter and populate the candidate models with the matching models from the datalayer.


## What it does

1. Reads the `model` field from the request body.
3. If the field is absent, an empty string or `auto`, all incoming candidates are kept.
3. If the `model` field is formatted as  `auto/group-name`, with the prefix `auto` and separator `/`, it extract `group-name` and looks up the group-name in the filter parameters. All candidate model names from the data layer that also appear in the group are kept.
4. If the `model` field is a valid non-empty string that does not start with the prefix `auto`, the model name is considered the only one that should be in the candidate list and kept.
5. If the intersection is empty or the field is malformed (not a string), the filter returns no candidates and the pipeline rejects the request with HTTP 429.

## Inputs consumed

- The `model` field of the request body.
- The candidate model list passed in by the pipeline.
- A set of groups with the associated models
  - Group names must be non-empty, and must contain at least one model name in the list.
  - Model names in the group list must be non-empty strings.
  - A group with an invalid name or an invalid/empty model list is skipped (not loaded) and a warning is logged; the rest of the configured groups are still loaded normally.

## Example configuration

```yaml
plugins:
- type: model-group-name-filter
  parameters:
    qwen3models:                # The group name
      - qwen3-8b                # Model name in the group 
      - qwen3-32b               # Model name in the group
```
The following configuration for the plugin defines a group `qwen3models` with two models `qwen3-8b` and `qwen3-32b`. The list of models is used to filter the candidates when the request has the `model` set to `auto/qwen3models`.
