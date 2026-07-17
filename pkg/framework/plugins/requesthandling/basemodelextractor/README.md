# Base Model Extractor

**Type:** `base-model-to-header`

A request processor that resolves LoRA adapter names to their base model and sets the result as a request header. This enables Envoy routing rules to route by base model even when the client specifies an adapter name.

## How it works

1. Reads the `model` field from the request body.
2. Looks up the model name in an in-memory `AdaptersStore` that maps adapter names to base model names.
3. If a mapping is found, sets the `X-Gateway-Base-Model-Name` header to the base model name.
4. If the model is not an adapter (no mapping found), the header is not set.

The `AdaptersStore` is populated by a Kubernetes ConfigMap reconciler that watches ConfigMaps labeled with `ipp-managed`. Each ConfigMap declares a `baseModel` and a list of `adapters`:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  labels:
    app.kubernetes.io/managed-by: ipp
data:
  config: |
    baseModel: llama-3.1-8b
    adapters:
      - sql-lora
      - code-lora
```

Multiple ConfigMaps can map different adapters to the same or different base models. The store updates dynamically as ConfigMaps are created, modified, or deleted.

## Configuration

### Parameters

None.

## Outputs produced

| Where | Contents |
|-------|----------|
| Request header `X-Gateway-Base-Model-Name` | The resolved base model name |
