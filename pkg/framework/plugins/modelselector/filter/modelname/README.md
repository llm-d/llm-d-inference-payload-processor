# Model Name Filter

Restricts the candidate models to the exact model name in the request body. The body's
model field is treated as a single model name.

It is registered as type `model-name-filter` and runs as a modelselector filter.

The filter matches the requested name against the candidate models the pipeline hands it from the datalayer. "Available" below means "present in that candidate list".

## What it does

1. Reads the `model` field from the request body.
2. When the field holds an available model name, that one model becomes the single candidate, which de facto means that model is selected (picker doesn't have alternatives).
3. If the field is absent or an empty string, all incoming candidates are kept.
4. If the requested model is not available, or the field is malformed (not a string), the filter returns no candidates and the pipeline rejects the request with HTTP 429.

## Inputs consumed

- The `model` field of the request body.
- The candidate model list passed in by the pipeline.
