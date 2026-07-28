# Model Config Datasource

Populates the datastore with model names, pricing, and group membership read from a JSON config
file, and keeps the datastore in sync as the file changes.

It is registered as type `model-config-datasource` and runs as a datalayer datasource.

## What it does

1. Reads a JSON file at `modelsPath` containing a `models` list and an optional `groups` list.
2. For each valid model entry, registers the model in the datastore via `GetOrCreateModel` and
   writes its pricing as a `pricing.TokenPricesAttributeKey` attribute (converted from USD per
   1M tokens to USD per token).
3. Inverts the group-centric `groups` list into per-model membership and writes each model's group
   names as a `modelgroups.GroupsAttributeKey` attribute. If a model's group memberships change on
   reload, the attribute is updated; if all memberships are removed, the attribute is deleted.
4. Removes any model from the datastore that no longer appears in the file.
5. Watches the file's **parent directory** for `Write`, `Create`, `Remove`, and `Rename` events,
   re-syncing on every relevant change.

The directory is watched rather than the file directly to handle atomic rename-based replacements,
such as Kubernetes ConfigMap remounts. A missing or empty config file — including when the file is
deleted or renamed away — is treated as an empty config: the datastore is immediately cleared of
every model. Models repopulate automatically once a valid file reappears at `modelsPath`.

## Behavioral Intent

This datasource is the authoritative source of which models the system knows about. On every file
change the datastore converges to exactly the set of models listed in the file — new models are
added, stale models are deleted, and attributes (pricing, group membership) are refreshed.

### Skip conditions (logged at info level)

| Condition | Action |
|-----------|--------|
| Model entry with an empty `name` | Skipped |
| Model entry with a negative `input_per_million` or `output_per_million` | Skipped; also excluded from group membership resolution |
| Group entry with an empty `name` or an empty `models` list | Skipped |
| Empty model name within an otherwise valid group entry | That name is skipped; rest of the group proceeds |
| Group entry referencing a model name absent from (or skipped in) `models` | Skipped; the unknown or invalid model name is logged |

## Config file format

```json
{
  "models": [
    { "name": "model-a" },
    { "name": "model-b", "pricing": { "input_per_million": 0.5, "output_per_million": 1.5 } }
  ],
  "groups": [
    { "name": "fast",     "models": ["model-a", "model-b"] },
    { "name": "cheap",    "models": ["model-a"] }
  ]
}
```

- `models` — required; each entry must have a non-empty `name`. `pricing` is optional; when omitted
  it defaults to zero (the model is treated as free).
- `pricing.input_per_million` / `pricing.output_per_million` — USD per 1,000,000 tokens. Must be
  ≥ 0 (negative prices cause the model entry to be skipped).
- `groups` — optional; each entry must have a non-empty `name` and a non-empty `models` list. A
  model can appear in more than one group.

## Configuration

| Field        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `modelsPath` | string | yes      | Path to the JSON models config file (see format above). Resolved to an absolute path at startup. Must be a file, not a directory. |

### Example plugin config

```json
{
  "type": "model-config-datasource",
  "name": "my-model-config",
  "config": {
    "modelsPath": "/etc/llm-d/models.json"
  }
}
```

## Inputs consumed

- The JSON file at `modelsPath` on disk.

## Outputs produced

- Models registered in (or removed from) the shared `Datastore`.
- `pricing.TokenPricesAttributeKey` (`"token_prices"`) — per-model input/output prices in USD per
  token, written on every sync. A model with no pricing block gets zero prices (free model).
- `modelgroups.GroupsAttributeKey` (`"model_groups"`) — per-model list of group names the model
  belongs to, written on every sync. The attribute is deleted when a model no longer belongs to any
  group. Consumed by `model-group-name-filter` to resolve `auto/<group-name>` selectors.
