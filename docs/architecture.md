# Architecture

## Components

```
┌──────────────┐   REST/SSE/WS   ┌──────────────────┐   REST proxy    ┌──────────────────┐
│ React SPA    │ ──────────────► │ server (Go)      │ ──────────────► │ agent (Go)       │
│ (served by   │                 │ - k8s CRUD       │  /api/v1/agent/*│ - monitor loop   │
│  the server) │                 │ - logs (SSE)     │                 │ - insight store  │
└──────────────┘                 │ - exec (WS)      │                 │ - query engine   │
                                 │ - events         │                 └───┬──────────┬───┘
                                 └───────┬──────────┘                     │          │
                                         │ client-go                      │client-go │ HTTP
                                         │ (read + update/patch,          │(read-    │
                                         │  pods/log, pods/exec)          │ only)    ▼
                                         ▼                                │   Claude API /
                                    Kubernetes API  ◄─────────────────────┘   Ollama
```

One Go module, two binaries:

| Binary | Package | Responsibility |
| --- | --- | --- |
| `cmd/server` | `internal/server`, `internal/k8s` | REST API over the cluster, SSE log streaming, WebSocket exec bridge, reverse proxy to the agent, static hosting of the built SPA |
| `cmd/agent` | `internal/agent`, `internal/llm` | Periodic cluster review (monitor loop), insight ring buffer, on-demand agentic queries with read-only tools |

Shared packages: `internal/k8s` (client-go wrapper), `internal/config`,
`internal/logging`.

## Why the agent is a separate service

1. **Independent failure domains** — a hung LLM call or provider outage never
   affects browsing resources or tailing logs.
2. **Least privilege** — the agent runs under its own ServiceAccount with a
   read-only ClusterRole. Even a prompt-injected or hallucinating model cannot
   mutate the cluster, because the credentials it runs with cannot.
3. **Independent lifecycle** — restart the agent to pick up a new model/key
   without dropping active exec sessions in the server.

The server proxies `/api/v1/agent/*` to the agent (`AGENT_URL`), so the
browser only ever talks to one origin.

## Resource access model

All resource operations go through the **dynamic client** with a static
kind→GVR registry (`internal/k8s/registry.go`). One code path handles every
kind; adding a kind is one registry entry plus an optional list-column
summarizer (`summarize.go`).

Reads return either trimmed summaries (list views) or the full object plus a
cleaned YAML manifest (detail view; `managedFields`, `resourceVersion`, `uid`
etc. stripped). Updates parse the edited YAML, verify kind/name/namespace
match the URL, re-attach the live `resourceVersion`, and issue an `Update` —
the same optimistic-concurrency window as `kubectl edit`.

## AI data flow

**Monitor loop** (every `AGENT_REVIEW_INTERVAL`, default 5m):

1. `internal/agent/snapshot.go` collects a *compact text snapshot*: counts,
   node conditions, unhealthy pods (with waiting reasons/restarts), degraded
   deployments, recent warning events. Full manifests are never sent.
2. One LLM call with a "cluster SRE reviewer" system prompt asks for a strict
   JSON verdict: `status` (healthy/warning/critical), `summary`, `findings[]`.
3. The parsed insight lands in an in-memory ring buffer (last 20 reviews) and
   is served via `GET /status` / `GET /insights`. A failed review is stored
   with `status: "error"` and the loop simply retries next tick.

**Query engine** (`POST /query`): a classic agentic tool loop. The model gets
five read-only tools (`get_cluster_overview`, `list_resources`,
`get_resource`, `get_pod_logs`, `get_events`) and iterates — capped at 15
iterations, 1000 log lines and 30KB per tool result — until it produces a
final answer. Progress (tool calls, text) streams to the UI as SSE events.

**Provider abstraction** (`internal/llm`): a single `Provider` interface
(`Chat(ctx, req) (*ChatResponse, error)`) with two implementations:

- `llm/anthropic` — official `anthropic-sdk-go`, default model
  `claude-opus-4-8`
- `llm/ollama` — raw HTTP against Ollama's `/api/chat` with function-calling,
  default model `qwen3`

The agent core is provider-agnostic; adding a provider means implementing one
interface and one switch arm in `cmd/agent/main.go`.

## RBAC model

| ServiceAccount | Scope | Verbs |
| --- | --- | --- |
| `server` | all browsable kinds + `pods/log` | get, list, watch, **update, patch** (YAML editor) |
| `server` | `pods/exec` | create (terminal) |
| `agent` | browsable kinds **except secrets** + `pods/log` | get, list, watch only |

The agent deliberately has no secrets access and no write verbs — verify with
`kubectl auth can-i delete pods --as=system:serviceaccount:kentinel:agent`.

## Frontend

React 18 + TypeScript + Vite + Tailwind. TanStack Query handles polling and
caching (10s for lists, 15s for agent status). Monaco (bundled locally, so
air-gapped clusters work; lazy-loaded to keep the initial bundle small) for
YAML editing, xterm.js for the terminal, SSE via `EventSource`/`fetch` streams
for logs and agent answers.
