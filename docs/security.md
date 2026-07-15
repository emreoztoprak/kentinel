# Security

Read this before running the console anywhere that isn't your own machine.

## The headline: v1 has no authentication

Anyone who can reach the server port can, within the server's RBAC scope:

- read every resource the UI shows (including Secret manifests via the YAML view),
- edit manifests,
- **exec into pods** — which is effectively code execution in your workloads.

Deploy accordingly:

| Mode | Guardrail |
| --- | --- |
| Docker | Ports bind to `127.0.0.1` only (see `docker-compose.yml`) |
| In-cluster | ClusterIP Services only, no Ingress shipped; access via `kubectl port-forward` (which itself requires kubeconfig credentials) |

Do not add an Ingress/LoadBalancer in front of this UI on a shared network.
Token auth and OIDC are on the roadmap; until then `kubectl port-forward` *is*
the auth layer.

## Two trust domains by design

| | server | agent |
| --- | --- | --- |
| Cluster access | get/list/watch + update/patch, `pods/log`, `pods/exec` | get/list/watch + `pods/log` **only** |
| Secrets | readable (needed for the resource browser) | **no access** |
| Talks to LLM | never | yes |

The consequence: **nothing that touches the LLM can mutate the cluster or
read secrets.** Even if a model hallucinates, is prompt-injected via pod logs
(logs are attacker-influenced input!), or a provider is compromised, the blast
radius is read-only cluster metadata and logs.

## What leaves your machine/cluster

With a cloud provider (`anthropic`, `openai`, `deepseek`, `gemini`), the
agent sends to that provider's API:

- the periodic snapshot (resource names, namespaces, counts, conditions,
  warning-event messages),
- for queries: your prompt plus tool outputs (which can include pod logs and
  resource manifests the model requested — but never Secret contents, since
  the agent has no secrets RBAC).

With `LLM_PROVIDER=ollama`, nothing leaves your infrastructure.

## Secret handling

- The agent's ClusterRole excludes `secrets` entirely.
- LLM API keys and notification webhooks can be set from the Settings page
  but are **write-only**: `GET /api/v1/agent/config` only ever reports
  set/not-set booleans. The agent holds the values in memory; in k8s mode
  the **server** (not the agent) persists newly entered values into the
  `agent-secrets-overrides` Secret — a separate object from the one below,
  so a value saved from the UI is never clobbered by a `helm upgrade` or
  `kubectl apply` (see [deployment.md](deployment.md)).
  Remember the flip side: since the UI has no auth, anyone who can reach it
  can *replace* the key or redirect the agent to their own Ollama host — one
  more reason to keep this behind `kubectl port-forward`.
- The `agent-secrets` Secret holds the install-time LLM API keys and webhook
  URLs (chart/manifest defaults); replace the committed `REPLACE_ME`
  placeholders out-of-band and never commit real values. Anything set later
  from the Settings UI lands in `agent-secrets-overrides` instead and takes
  precedence.
- The UI's Secret YAML view shows base64 data as stored — treat UI access as
  secret access when deciding who may reach the port.

## Hardening already in place

- Both containers: distroless base, non-root, read-only rootfs, all
  capabilities dropped, resource limits.
- Manifest updates verify kind/name/namespace match the URL (no cross-object
  writes from a stale editor tab).
- Exec/log/tool inputs are capped (lines, bytes, iterations) to bound abuse.
- WebSocket exec sessions die with the connection; stdin closes the shell.
- The exec WebSocket checks `Origin` against the request host, so a
  malicious website you happen to have open can't script a connection to
  your local Kentinel instance (cross-site WebSocket hijacking) — this is a
  best-effort layer, not a substitute for keeping the port off shared
  networks.

## Reporting

It's a hobby project — open an issue. Don't run it on the internet.
