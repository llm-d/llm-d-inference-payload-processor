# Payload Processor

A Helm chart for the payload processor deployment and service.


## Install

To install the payload processor, you can run the following command:

```txt
$ helm install payload-processor ./config/charts/payload-processor \
    --set provider.name=[gke|istio] \
    --set inferenceGateway.name=inference-gateway
```

Note that the provider name is needed to ensure provider-specific manifests are also applied. If no provider is specified, then only
the deployment and service are deployed.

To install via the latest published chart in staging  (--version v0 indicates latest dev version), you can run the following command:

```txt
$ helm install payload-processor oci://ghcr.io/llm-d/charts/payload-processor \ 
    --version v0
    --set provider.name=[gke|istio]
```

### Install with Custom Cmd-line Flags

To set cmd-line flags, you can use the `--set` option to set each flag, e.g.,:

```txt
$ helm install payload-processor ./config/charts/payload-processor \
    --set provider.name=[gke|istio] \
    --set inferenceGateway.name=inference-gateway
    --set payloadProcessor.flags.<FLAG_NAME>=<FLAG_VALUE>
```

Alternatively, you can define flags in the `values.yaml` file:

```yaml
payloadProcessor:
  flags:
    FLAG_NAME: <FLAG_VALUE>
    v: 3 ## Log verbosity
    ...
```

### Install with Custom Payload Processor Plugins Configuration

To set custom payload processor plugin config, you can pass it under plugins section. For example:
```yaml
payloadProcessor:
  plugins:
    - type: custom-plugin-type
      name: custom-plugin-name
      json: // optional, can be empty
        custom_param: "example-value"
    - type: ...
```

### Configure ext_proc Events

By default, the payload processor receives all HTTP lifecycle events (request and response headers, body, trailers). If your plugins only need specific events, you can disable the others to reduce latency:

```bash
# Disable response events if plugins only need request data
$ helm install payload-processor ./config/charts/payload-processor \
    --set provider.name=istio \
    --set provider.supportedEvents.responseHeaders=false \
    --set provider.supportedEvents.responseBody=false \
    --set provider.supportedEvents.responseTrailers=false
```

Or in `values.yaml`:
```yaml
provider:
  name: istio
  supportedEvents:
    requestHeaders: true
    requestBody: true
    requestTrailers: true
    responseHeaders: false  # Disable if plugins don't need response headers
    responseBody: false     # Disable if plugins don't need response body
    responseTrailers: false
```

> **Tip:** Only enable events your plugins need. Each extra event adds a network hop between the proxy and the payload processor.

### Uninstall

Run the following command to uninstall the chart:

```txt
$ helm uninstall payload-processor
```

## Configuration

The following table list the configurable parameters of the chart.

| **Parameter Name**               | **Description**                                                                                                                                                                                                                                                                                  |
|----------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `payloadProcessor.name`                   | Name for the deployment and service.                                                                                                                                                                                                                                                             |
| `payloadProcessor.replicas`               | Number of replicas for the deployment. Defaults to `1`.                                                                                                                                                                                                                                          |
| `payloadProcessor.port`                   | Port serving ext_proc. Defaults to `9004`.                                                                                                                                                                                                                                                       |
| `payloadProcessor.healthCheckPort`        | Port for health checks. Defaults to `9005`.                                                                                                                                                                                                                                                      |
| `payloadProcessor.multiNamespace`         | Boolean flag to indicate whether the payload processor should watch cross namespace configmaps or only within the namespace it is deployed. Defaults to `false`.                                                                                                                                                   |
| `payloadProcessor.image.repository`       | Repository of the container image used.                                                                                                                                                                                                                                                           |
| `payloadProcessor.image.registry`         | Registry URL where the image is hosted.                                                                                                                                                                                                                                                          |
| `payloadProcessor.image.tag`              | Image tag.                                                                                                                                                                                                                                                                                       |
| `payloadProcessor.image.pullPolicy`       | Image pull policy for the container. Possible values: `Always`, `IfNotPresent`, or `Never`. Defaults to `Always`.                                                                                                                                                                                |
| `payloadProcessor.flags`                  | map of flags which are passed through to the payload processor. Refer to [runner.go](https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/cmd/payload-processor/runner/runner.go) for complete list.                                                                                                     |
| `payloadProcessor.plugins`   | Custom ordered plugins array to set for the payload processor. Each plugin has fields: type, name and optionally json (which represents parameters of the plugin). If not specified, the payload processor will use by default the `body-field-to-header` to extract the `model` field, and `base-model-to-header` (in that order). |
| `provider.name`              | Name of the Inference Gateway implementation being used. Possible values: `istio`, `gke`. Defaults to `none`.                                                                                                                                                                                    |
| `provider.supportedEvents.requestHeaders` | Enable Request Headers event. Defaults to `true`. |
| `provider.supportedEvents.requestBody` | Enable Request Body event. Defaults to `true`. |
| `provider.supportedEvents.requestTrailers` | Enable Request Trailers event. Defaults to `true`. |
| `provider.supportedEvents.responseHeaders` | Enable Response Headers event. Defaults to `false`. |
| `provider.supportedEvents.responseBody` | Enable Response Body event. Defaults to `false`. |
| `provider.supportedEvents.responseTrailers` | Enable Response Trailers event. Defaults to `false`. |
| `inferenceGateway.name`      | The name of the Gateway. Defaults to `inference-gateway`.                                                                                                                                                                                                                                       |

## Notes

This chart should only be deployed once per Gateway.
