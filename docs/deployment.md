# Deployment

Two supported modes: **Docker** (local development machine, talks to any
cluster your kubeconfig can reach) and **in-cluster** (plain Kubernetes
manifests). Both run the same two images.

## Docker mode

Prerequisites: Docker with Compose, a kubeconfig, and (for the default
provider) an Anthropic API key.

```sh
cd deploy/docker
export ANTHROPIC_API_KEY=sk-ant-...
docker compose up --build
# UI: http://localhost:8080  (bound to 127.0.0.1 only)
```

Options (env vars read by the compose file):

| Variable | Default | Purpose |
| --- | --- | --- |
| `KUBECONFIG_FILE` | `~/.kube/config` | Which kubeconfig to mount (read-only) |
| `LLM_PROVIDER` | `anthropic` | `anthropic` or `ollama` |
| `LLM_MODEL` | provider default | Model override |
| `AGENT_REVIEW_INTERVAL` | `5m` | Review cadence |

### Using Ollama

```sh
LLM_PROVIDER=ollama docker compose --profile ollama up --build
docker compose exec ollama ollama pull qwen3   # once, ~GBs
```

### Docker mode with kind/minikube

The kubeconfig is mounted *into containers*, so an API server address of
`127.0.0.1:<port>` won't resolve. Two options:

1. **kind**: create the cluster with an address the containers can reach:

   ```sh
   kind create cluster --config - <<'EOF'
   kind: Cluster
   apiVersion: kind.x-k8s.io/v1alpha4
   networking:
     apiServerAddress: "0.0.0.0"
   EOF
   ```

   then edit the mounted kubeconfig copy to point at
   `https://host.docker.internal:<port>` (the compose file already adds the
   `host.docker.internal` host mapping), and set
   `KUBECONFIG_FILE=/path/to/edited/config`. You may need
   `insecure-skip-tls-verify: true` for that context since the certificate
   doesn't include that hostname.

2. **Simpler**: skip Docker mode for kind and either run `make dev` natively
   or deploy in-cluster (below) — that's the mode kind exists for anyway.

## In-cluster mode — Helm (recommended)

```sh
helm install kentinel oci://ghcr.io/emreoztoprak/charts/kentinel \
  -n kentinel --create-namespace
kubectl -n kentinel port-forward svc/kentinel-server 8080:80
```

Key values (full reference: `charts/kentinel/values.yaml`):

| Value | Default | Purpose |
| --- | --- | --- |
| `llm.provider` | `ollama` | `ollama`, `anthropic`, `openai`, `deepseek`, `gemini` |
| `llm.apiKeys.<provider>` | — | API key for the chosen cloud provider |
| `ollama.enabled` | `true` | Bundled local LLM (disable when using a cloud provider); `ollama.externalHost` points at your own Ollama |
| `prometheus.enabled` | `true` | Bundled metrics source; disable + set `prometheus.externalUrl` to reuse yours |
| `agent.reviewInterval` | `5m` | Review cadence (cost lever for cloud providers) |
| `agent.persistence.enabled` | `true` | Insight history PVC |
| `notifications.*` | off | Discord/Slack/Teams webhooks + severity threshold |

Upgrades: `helm upgrade kentinel oci://ghcr.io/emreoztoprak/charts/kentinel -n kentinel`.
Settings changed in the UI persist to the release's ConfigMap/Secret — a
`helm upgrade` with explicit `--set` values for those fields overwrites them.

## In-cluster mode — raw manifests

Prerequisites: `kubectl` against the target cluster, images available to the
cluster (kind load, or push to a registry and adjust the `image:` fields).

```sh
# kind: build images, load them, apply everything
make kind-deploy

# minikube: build inside minikube's Docker daemon, apply everything
make minikube-deploy MINIKUBE_PROFILE=my-profile

# Other clusters: build, tag, push to your registry, adjust image: fields, then
kubectl apply -f deploy/k8s/
```

What gets created (namespace `kentinel`):

| Object | Notes |
| --- | --- |
| ServiceAccounts `server`, `agent` + ClusterRoles/Bindings | Split RBAC: server can update/patch + exec; agent is read-only, no secrets |
| ConfigMap `agent-config` | Provider (default `ollama`), model, review interval |
| Secret `agent-secrets` | `ANTHROPIC_API_KEY` (placeholder — only needed for the anthropic provider) |
| Deployments `server`, `agent` | Distroless, non-root, read-only rootfs, probes, resource limits |
| Deployment `ollama` + PVC + Service | Local LLM (default provider); auto-pulls `qwen3:0.6b` on first boot (~1.5GB RAM — check node headroom). Delete it if you use anthropic |
| Deployment `prometheus` + PVC + Service + RBAC | Minimal metrics source for the agent (kubelet scrape only, 7d retention). Have your own Prometheus? Point the agent at it (Settings → Metrics) and delete this one — commands in `06-prometheus.yaml` |
| PVC `agent-data` | Insight history database (block storage recommended; avoid NFS) |
| Services `server` (:80), `agent` (:8090) | ClusterIP only, no Ingress |

The default provider is the in-cluster Ollama — it works out of the box, no
key needed. The very first review can take a few minutes while the model
downloads and loads.

Switch to Anthropic Claude (better analysis quality):

```sh
kubectl -n kentinel create secret generic agent-secrets \
  --from-literal=ANTHROPIC_API_KEY=sk-ant-... \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n kentinel patch configmap agent-config --type merge \
  -p '{"data":{"LLM_PROVIDER":"anthropic","LLM_MODEL":""}}'
kubectl -n kentinel rollout restart deploy/agent
# optional: free the ollama resources
kubectl -n kentinel delete deploy/ollama svc/ollama pvc/ollama-models
```

Access the UI (intentionally not exposed publicly — see
[security.md](security.md)):

```sh
kubectl -n kentinel port-forward svc/server 8080:80
# http://localhost:8080
```

### Upgrading

Rebuild images, reload/push, then:

```sh
kubectl -n kentinel rollout restart deploy/server deploy/agent
```

Manifest changes: `kubectl apply -f deploy/k8s/` is idempotent — except the
`agent-secrets` placeholder, which would overwrite your real key. Apply
selectively or re-create the secret afterwards.

### Uninstalling

```sh
kubectl delete -f deploy/k8s/
```

## Health checks

- Server: `GET /healthz` on :8080
- Agent: `GET /healthz` on :8090 (proxied at `/api/v1/agent/healthz`)

## Troubleshooting

| Symptom | Likely cause / fix |
| --- | --- |
| Dashboard loads, AI panel says "agent not reachable" | Agent pod not running, or `AGENT_URL` wrong. `kubectl -n kentinel logs deploy/agent` |
| AI panel shows status `error` with an API error | Bad/missing `ANTHROPIC_API_KEY`, or Ollama host/model unavailable. The exact error is in the panel and agent logs |
| `fatal: connecting to kubernetes` on startup | No kubeconfig found (Docker: check the mount; local: check `KUBECONFIG`) |
| YAML apply fails with 403 | The server's ClusterRole doesn't cover that kind/verb — extend `deploy/k8s/01-rbac.yaml` |
| Exec tab connects then closes immediately | The container has no `/bin/sh` (distroless images) — try another container, or the pod is not Running |
| Docker mode can't reach kind cluster | kubeconfig points at 127.0.0.1 — see "Docker mode with kind/minikube" above |
