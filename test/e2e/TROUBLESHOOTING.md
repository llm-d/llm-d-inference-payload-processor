# E2E Troubleshooting

## Envoy routes to the wrong backend

If you have other Kind clusters running, Envoy may discover
endpoints from those clusters. Delete all Kind clusters and
re-create only the e2e cluster:

```bash
kind delete clusters --all
make test-e2e
```

## Tests fail during setup (pods not ready)

Check pod status in the e2e namespace:

```bash
kubectl get pods -n ipp-e2e
kubectl describe pod -n ipp-e2e <pod-name>
```

## Manually inspect the cluster after a run

Set `E2E_PAUSE_ON_EXIT=30m` to keep the cluster alive after
tests complete:

```bash
E2E_PAUSE_ON_EXIT=30m make test-e2e
```

## Clean up the Kind cluster

```bash
kind delete cluster --name ipp-e2e
```
