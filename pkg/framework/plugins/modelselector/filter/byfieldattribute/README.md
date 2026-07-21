# By-Field Filter (and Model Name Filter)

Restricts the candidate models based on a configurable field in the request body, compared
against either the candidate model's name or one of its attributes.

The filter matches the requested value against the candidate models the pipeline hands it from
the datalayer. "Available" below means "present in that candidate list".

## `by-field-filter`

The generic filter. Registered as type `by-field-filter` and runs as a modelselector filter.

## What it does

1. Reads the configured `fieldName` field from the request body.
2. Compares that value against each candidate's comparison value:
   - By default, the comparison value is the candidate's name (`Model.GetName()`).
   - When `byField` is set, the value compares with the `byField` value attribute for
     the model, which is defined in the candidate's `AttributeMap`.
3. Returns every candidate whose comparison value matches. Candidates that don't carry the
   `byField` attribute (when configured) are skipped, not treated as an error.
4. If the field is absent or an empty string, all incoming candidates are kept.
5. If no candidate matches, or the field is malformed (not a string), the filter returns no
   candidates and the pipeline rejects the request with HTTP 429.

## Configuration

- `fieldName` (required) — the request-body field to read the comparison value from.
- `byField` (optional) — the name of the candidate attribute to compare against.
  When empty or omitted, defaults to the candidate's name (`Model.GetName()`).

## Inputs consumed

- The `fieldName` field of the request body.
- The candidate model list passed in by the pipeline, including each candidate's attributes (when
  `byField` is configured).

## Example configuration: filter by model name

Equivalent to the built-in `model-name-filter` (see below):

```yaml
plugins:
- type: by-field-filter
  params:
    fieldName: model
```

## Example configuration: filter by a custom field/attribute

For example, filtering Anthropic-on-Bedrock requests by their `anthropic_version` field, matched
against a candidate attribute named `anthropicVersion` (populated by another plugin, e.g. the
`model-config-datasource` plugin, onto each candidate's `AttributeMap`):

```yaml
plugins:
- type: by-field-filter
  params:
    fieldName: anthropic_version
    byField: anthropicVersion
```

## `model-name-filter`

Kept as a separate, backward-compatible plugin type with the original, fixed behavior: it always
reads the `model` field and compares by the candidate's name, ignoring any parameters passed to it.

It is registered as type `model-name-filter` and behaves exactly like `by-field-filter` configured
with `fieldName: model` and no `byField` override.

```yaml
plugins:
- type: model-name-filter
```
