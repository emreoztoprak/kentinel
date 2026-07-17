# API Reference

Base URL: the server (default `:8080`). All responses are JSON unless noted.
Errors use a consistent envelope with a matching HTTP status:

```json
{ "error": "not_found", "message": "pods \"foo\" not found" }
```

Kubernetes API semantics are preserved: 404 (missing), 403 (RBAC), 409
(conflict on save), 422 (validation), 400 (bad input), 502 (agent
unreachable).

## Cluster

| Method & path | Description |
| --- | --- |
| `GET /healthz` | Server liveness |
| `GET /api/v1/overview` | Dashboard stats: node/pod/namespace/deployment counts, pod phases, last 10 warning events |
| `GET /api/v1/namespaces` | `{"namespaces": ["default", ...]}` |
| `GET /api/v1/kinds` | Supported resource kinds with display names |
| `GET /api/v1/events?namespace=&type=` | Events, newest first. `type` = `Normal` \| `Warning` |

## Resources

Supported `{kind}` values: `pods`, `deployments`, `statefulsets`,
`daemonsets`, `services`, `configmaps`, `secrets`, `ingresses`,
`persistentvolumeclaims`, `jobs`, `cronjobs`, `nodes`.

| Method & path | Description |
| --- | --- |
| `GET /api/v1/resources/{kind}/?namespace=` | List with kind-specific status columns (`extra`) |
| `GET /api/v1/resources/{kind}/{namespace}/{name}` | Detail: metadata + full object + cleaned YAML. Use `-` as namespace for cluster-scoped kinds |
| `PUT /api/v1/resources/{kind}/{namespace}/{name}` | Apply edited YAML. Body: `{"yaml": "..."}`. The manifest's kind/name/namespace must match the path |

## Pods

| Method & path | Description |
| --- | --- |
| `GET /api/v1/pods/{ns}/{name}/containers` | Container names (init containers last) |
| `GET /api/v1/pods/{ns}/{name}/logs?container=&tailLines=&sinceSeconds=&previous=` | Plain-text log tail (max 5000 lines) |
| `GET /api/v1/pods/{ns}/{name}/logs?follow=true&...` | **SSE** stream; `event: log` per line, `event: error` on failure |
| `GET /api/v1/pods/{ns}/{name}/exec?container=&command=` | **WebSocket** terminal (see protocol below) |

### Exec WebSocket protocol

JSON text frames both ways:

```
client → server  {"type":"stdin","data":"ls\r"}
client → server  {"type":"resize","cols":120,"rows":32}
server → client  {"type":"stdout","data":"..."}
server → client  {"type":"error","data":"..."}      (setup/stream failure)
server → client  {"type":"exit"}                     (session ended)
```

`command` defaults to `/bin/sh`; the session is a TTY, stderr is merged.

## AI agent (proxied)

Everything under `/api/v1/agent/*` is forwarded to the agent service with the
prefix stripped.

| Method & path | Description |
| --- | --- |
| `GET /api/v1/agent/healthz` | Agent liveness |
| `GET /api/v1/agent/status` | `{provider, model, latest}` — `latest` is the most recent insight or `null` |
| `GET /api/v1/agent/insights?limit=&status=&since=&until=` | Review history, newest first. `limit` default 50 / max 500; `status` = healthy\|warning\|critical\|error; `since`/`until` RFC3339. Response includes `persistent: bool` |
| `GET /api/v1/agent/insights/timeline?hours=24` | Compact trend points `[{t, status}]`, oldest first, max 168h |
| `POST /api/v1/agent/query` | Body `{"prompt":"..."}` (max 8000 chars). **SSE** response |
| `GET /api/v1/agent/config` | Runtime agent settings. The API key is reduced to `anthropicKeySet: bool` — never returned |
| `PUT /api/v1/agent/config` | Update settings (see below). Live-applies on the agent, then persists encrypted to the agent's own database. The only way to change settings after the agent's first boot — see [security.md](security.md) |
| `GET /api/v1/settings` | Server's own read-only settings (`agentUrl`, `staticDir`, `inCluster`, `namespace`, `version`) |
| `GET /api/v1/agent/models?provider=&host=` | Selectable models: installed Ollama models or the curated Claude list |
| `POST /api/v1/agent/notifications/test` | Send a test notification to every configured webhook (Discord/Slack/Teams) |
| `GET /api/v1/agent/metrics/health` | Check Prometheus connectivity (400 = not configured, 502 = unreachable) |

### PUT /api/v1/agent/config

```json
{
  "provider": "ollama",            // "ollama" | "anthropic" | "openai" | "deepseek" | "gemini"
  "model": "qwen3:0.6b",           // empty = provider default
  "ollamaHost": "http://ollama.kentinel.svc:11434",
  "apiKey": "sk-...",              // optional, write-only; applies to the selected provider; empty = keep existing
  "reviewInterval": "5m",          // Go duration, min 30s
  "monitorEnabled": true,
  "notificationsEnabled": false,
  "discordWebhookUrl": "https://discord.com/api/webhooks/...", // optional, write-only
  "slackWebhookUrl": "https://hooks.slack.com/services/...",   // optional, write-only
  "teamsWebhookUrl": "https://...",                            // optional, write-only (Workflows webhook)
  "notifyMinSeverity": "warning",  // "warning" | "critical"
  "prometheusUrl": "http://prometheus.kentinel.svc:9090", // plain field; empty DISABLES metrics
  "insightRetentionDays": 90            // review-history retention, 1–3650; 0 = leave unchanged
}
```

`GET /config` masks all secrets: API keys come back as
`apiKeysSet: {"anthropic": true, "openai": false, ...}` and webhooks as
`discordWebhookSet` / `slackWebhookSet` / `teamsWebhookSet` booleans.

Response: the new settings view, same shape as `GET /config`, plus
`"persistent": bool` — whether this state survives a pod restart (a working
database + encryption key; false in Docker mode without `INSIGHT_DB_PATH`).
Validation errors (bad interval, missing key for anthropic, unknown provider)
return 400 with the agent's message; nothing is changed in that case.

### Query SSE events

Each frame is `data: {json}`:

```json
{"type":"tool","content":"get_pod_logs {\"namespace\":\"app\",\"pod\":\"demo-1\"}"}
{"type":"text","content":"The pod is failing because..."}
{"type":"done","content":""}
{"type":"error","content":"LLM request failed: ..."}
```

### Insight shape

```json
{
  "status": "warning",
  "summary": "One deployment is degraded.",
  "findings": [
    {
      "severity": "warning",
      "resource": "deployment app/demo-app",
      "title": "Replicas unavailable",
      "detail": "1/3 replicas available; pods are in ImagePullBackOff.",
      "recommendation": "Check the image tag: kubectl -n app describe pod ..."
    }
  ],
  "createdAt": "2026-07-07T10:00:00Z",
  "durationMs": 3200,
  "provider": "anthropic",
  "model": "claude-opus-4-8",
  "reviewError": ""
}
```
