# The AI Agent

The agent is a separate Go service with two jobs: a **monitor loop** that
periodically reviews the cluster, and a **query engine** that answers ad-hoc
questions. Both share one LLM provider and a read-only Kubernetes client.

## Monitor loop

Every `AGENT_REVIEW_INTERVAL` (default 5m, first run at startup):

1. **Snapshot** — the agent collects a compact plain-text summary of the
   cluster (`internal/agent/snapshot.go`): counts, node conditions, unhealthy
   pods with waiting reasons and restart counts, degraded deployments, and
   the latest warning events. Lists are capped (30 pods, 20 events) so the
   prompt stays small even in noisy clusters. Full manifests and secret data
   are never included.
2. **Review** — one LLM call with an SRE-reviewer system prompt that demands a
   strict JSON response: `status` (healthy/warning/critical), `summary`, and
   `findings[]` (severity, resource, title, detail, recommendation).
3. **Store** — the parsed insight goes into an in-memory ring buffer (last
   20). `GET /status` returns the latest (this drives the dashboard panel),
   `GET /insights` returns the history.

Failure handling: if the snapshot, the LLM call, or JSON parsing fails, an
insight with `status: "error"` and the underlying message is stored, the error
is logged, and the loop simply retries next tick. The agent process never
crashes because a review failed.

Parsing is deliberately lenient — models occasionally wrap JSON in prose or
code fences, so the parser extracts the first top-level JSON object it finds
and validates the fields.

## Query engine

`POST /query {"prompt": "..."}` starts an agentic loop:

```
user prompt ─► LLM ─► tool calls? ─► run tools (read-only) ─► results ─► LLM ─► ... ─► final answer
```

## Assisted mode: remediation proposals

When deployed with `mode: assisted` (see [security.md](security.md)), the
query engine gains one extra tool, `propose_change`. If you ask the assistant
to fix or change something, it reads the current manifest, then calls
`propose_change` with the full modified manifest and a rationale. This makes
**no change** — it records a *pending proposal* in the agent's database.

The proposal is shown as an **inline approval card** right in the chat — with
the rationale and a full diff, plus Approve/Reject — so you decide without
leaving the conversation. (The dashboard's **Pending changes** panel is the
cross-conversation view of everything still awaiting approval.) On approval,
the **server** (which holds the write RBAC — the agent never does) applies it
via the same guarded path as the manifest editor and records the result.
Rejecting one is a no-op on the cluster. The status transitions (pending →
applied/failed/rejected) with timestamps are the audit trail.

In `readonly` mode the tool doesn't exist and the assistant only advises in
text. Proposal generation is currently assistant-driven (you ask, it
proposes); review-initiated proposals are a future enhancement.

Available tools (all read-only, `internal/agent/tools.go`):

| Tool | What it does |
| --- | --- |
| `get_cluster_overview` | Counts, pod phases, recent warnings |
| `list_resources` | List any supported kind, with status columns |
| `get_resource` | One resource's full YAML (incl. status) |
| `get_pod_logs` | Tail logs; supports container, `sinceSeconds`, `previous` |
| `get_events` | Events filtered by namespace/type |
| `get_resource_usage`* | Actual CPU/memory usage: top pods, node usage, CPU-throttled containers |
| `query_metrics`* | Raw PromQL instant query — trends via `rate()`/`avg_over_time()` |

\* only present when a Prometheus URL is configured — see Metrics below.

Guardrails:

- **No write tools exist.** The agent's RBAC is also read-only in-cluster, so
  even a malicious prompt can't mutate anything.
- Max **15 tool iterations** per query, **5 minute** total timeout.
- Log fetches capped at 1000 lines / 1MB; every tool result truncated to 30KB
  before it reaches the model.
- Tool errors (wrong namespace, RBAC denied, missing pod) are fed back to the
  model as error results so it can adjust course instead of failing the query.

Progress streams to the UI as SSE events: `text` (assistant output), `tool`
(a tool call is running), then `done` or `error`. Responses stream **per
step**, not per token — a deliberate simplification that keeps both provider
adapters trivial.

## Prompts

Both system prompts live in code:

- Review prompt: `internal/agent/monitor.go` (`reviewSystemPrompt`)
- Query prompt: `internal/agent/query.go` (`querySystemPrompt`)

Tuning tips if you customize them:

- Keep the JSON contract in the review prompt exact — the parser validates
  `status` and `summary`.
- The "Completed Jobs are normal" line exists because models otherwise flag
  every finished Job as a problem.
- For the query prompt, keep the "ground every claim in tool output" rule; it
  is the main defense against hallucinated diagnoses.

## Metrics (Prometheus)

Without metrics the agent sees state, events, and logs — but not what
resources are actually doing. Whole problem classes are invisible that way:
CPU throttling (pod Running, logs clean, latency terrible), memory creep
before the OOMKill, node pressure, over-provisioning. With a Prometheus URL
configured the agent gains:

- **Two read-only tools**: `get_resource_usage` (canned overview: top pods by
  memory/CPU, node usage, throttled containers) and `query_metrics` (raw
  PromQL for anything else, including trends via `rate()` / `avg_over_time()`)
- **A metrics section in every review snapshot** (node memory, top consumers,
  throttled containers) — so the periodic review can warn *before* things
  crash, not after

**Metrics source.** The k8s manifests bundle a minimal single-instance
Prometheus (`deploy/k8s/06-prometheus.yaml`) that scrapes only the kubelets
(resource metrics + cAdvisor) — pod/node CPU & memory and CPU throttling,
7-day retention, ~128–512MB RAM. It is intentionally not a full monitoring
stack.

**Already run Prometheus?** Point the agent at it instead: Settings →
Metrics → Prometheus URL (there's a "Test connection" button), or set
`PROMETHEUS_URL`. Then delete the bundled one — removal commands are in the
header of `06-prometheus.yaml`. Any Prometheus works; the canned
`get_resource_usage` queries expect the kubelet resource metric names
(`pod_memory_working_set_bytes` etc., standard in kubelet scrape configs),
while `query_metrics` works against whatever metrics you have.

Everything degrades gracefully: unreachable Prometheus → the review snapshot
carries a "metrics unavailable" note and tool calls return a clear error the
model can relay; empty URL → the metrics tools simply don't exist for the
model.

## Insight history

Every review result is kept in two places: an in-memory ring buffer (the last
20, powering the dashboard panel) and — when `INSIGHT_DB_PATH` is set — a
**SQLite database**, so history survives pod restarts. The k8s manifests
enable this by default (a 1Gi `agent-data` PVC mounted at `/data`); Docker
mode uses a named volume. Without a path the agent runs memory-only and the
UI says so.

What it powers:

- **Dashboard timeline strip** — the last 24h of reviews as a colored bar
  (green/amber/red), so recurring degradation windows are visible at a glance
- **AI History page** — browse and filter every past review with its
  findings ("show me every critical this week")
- After a restart, the latest reviews are reloaded from disk, so the
  dashboard has history immediately

Implementation notes (and why SQLite is not a bottleneck here):

- Write load is one insert per review interval — orders of magnitude below
  SQLite's capacity. WAL mode is enabled so reads never block the writer.
- The agent is **single-writer by design** (`replicas: 1`, `Recreate`
  strategy, RWO PVC) — multiple agents would also duplicate reviews and
  notifications, so this is inherent, not a storage limitation.
- **Use block storage for the PVC** (the default on most clouds and
  minikube). Avoid NFS-backed storage classes — SQLite file locking is
  unreliable on network filesystems.
- Retention: rows older than the configured window (default 90 days) are
  pruned automatically on each insert. At a 5m interval, 90 days ≈ 26k rows
  ≈ a few MB — well within the 1Gi `agent-data` PVC. Change it any time from
  the Settings page (**Review history retention**, 1–3650 days); the change
  applies live and, like every other setting, persists across restarts.
  `INSIGHT_RETENTION_DAYS` only seeds the value on the agent's first boot.
- If the database cannot be opened the agent logs a warning and falls back
  to memory-only — persistence never takes the agent down.

## Notifications (Discord, Slack, Teams)

The agent can push alerts to Discord, Slack, and/or Microsoft Teams via
webhooks — configured on the Settings page (Notifications section) or via
env vars (`NOTIFICATIONS_ENABLED`, `DISCORD_WEBHOOK_URL`,
`SLACK_WEBHOOK_URL`, `TEAMS_WEBHOOK_URL`, `NOTIFY_MIN_SEVERITY`). Every
configured channel receives every alert; per-channel failures are logged and
never affect the review loop or the other channels.

Channel formats: Discord gets a color-coded embed, Slack a colored
attachment (incoming webhook), Teams an Adaptive Card (compatible with the
go-forward "Workflows" webhooks — Teams → channel → Workflows → *"Post to a
channel when a webhook request is received"*).

**When does it fire?** Only on status *transitions* between reviews:

| Transition | `warning` threshold | `critical` threshold |
| --- | --- | --- |
| healthy → warning | 🔔 alert | silent |
| warning → critical | 🔔 escalation | 🔔 alert |
| critical → warning | 🔔 downgrade | 🔔 downgrade |
| warning/critical → healthy | 🔔 recovery | 🔔 only if coming from critical |
| same status as last review | silent | silent |
| review `error` (LLM/cluster failure) | silent (visible in UI) | silent |

No repeat alerts while the status stays the same — dedup is the feature. The
message is a color-coded embed with the summary and the top findings.

**Setting it up:** in Discord, open the target channel's settings (⚙) →
Integrations → Webhooks → New Webhook → Copy Webhook URL. Paste it into
Settings → Notifications, enable, save, then click **Send test notification**
to verify the channel. The webhook URL is write-only (never returned by the
API) and persisted, encrypted, to the agent's own database — like every
setting changed from the UI (see [security.md](security.md)).

## Daily report

Independently of transition alerts, the agent can send a once-a-day digest
of the last 24 hours to the same webhooks — enable it in Settings →
Notifications (or `REPORT_ENABLED` / `REPORT_TIME`, "HH:MM" UTC). It
contains:

- **Reviews** — how many ran, the healthy/warning/critical breakdown, and
  how many times the status dipped below healthy (incidents)
- **Now** — the latest review's status and summary
- **Changes** — every remediation proposal created or decided in the window,
  with its outcome (`applied`, `rejected`, `pending`, `failed`). The report
  is the *record* of what changed — every applied change still went through
  an explicit human approval (see [security.md](security.md))
- **LLM usage** — calls and tokens over the window, with an estimated cost
  for priced cloud providers

The report is composed entirely from data the agent already stores (review
history, proposals, token usage) — it makes **no extra LLM calls** and no
cluster requests, so it's free to run even on cloud providers. **Send report
now** on the Settings page delivers today's digest immediately, schedule or
not. At most one scheduled report is sent per UTC day; if the agent pod is
down at the send minute, that day's report is skipped, not queued.

## Providers

`internal/llm.Provider` is the seam:

```go
type Provider interface {
    Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
    Name() string
    Model() string
}
```

| Provider | Package | Default model | Notes |
| --- | --- | --- | --- |
| Ollama (default) | `internal/llm/ollama` | `qwen3` (`qwen3:0.6b` in the k8s manifests) | Local/free; model must support tool calling; small models are weaker at structured output |
| Anthropic | `internal/llm/anthropic` | `claude-opus-4-8` | Official Go SDK; best quality for both loop and queries |
| OpenAI (ChatGPT) | `internal/llm/openaicompat` | `gpt-5.1` | OpenAI chat-completions protocol |
| DeepSeek | `internal/llm/openaicompat` | `deepseek-chat` | Natively OpenAI-compatible API |
| Google Gemini | `internal/llm/openaicompat` | `gemini-2.5-flash` | Via Google's official OpenAI-compatibility endpoint |

OpenAI, DeepSeek, and Gemini share one adapter (`openaicompat`) because all
three speak the OpenAI chat-completions wire protocol — adding another
compatible provider (vLLM, an internal LLM gateway, ...) is one `Preset`
entry. A genuinely different protocol means implementing the `llm.Provider`
interface.

## Cost & sizing notes

The dashboard shows an **LLM usage** card — token counts (input/output, split
by the review loop vs. the assistant) over the last 30 days, plus an
**estimated** cost for priced cloud providers. Local Ollama shows tokens only
(no API cost). Token counts come from each provider's own API response; the
cost is `tokens × published per-model price` (`GET /api/v1/agent/usage`).

The estimate is deliberately rough and **will not match your invoice**: prices
are hardcoded (and drift when providers change them), prompt-caching and batch
discounts aren't modeled, and it counts **only Kentinel's own calls** — it is
not your whole-account spend (an inference API key can't read billing anyway).

Where the tokens go:

- The **review loop dominates cost** because it runs 24/7:
  `calls/month ≈ 43800 / interval_minutes` (default 5m ≈ 8,600 calls/month).
  Each call sends a compact snapshot (typically 1–3K input tokens) and gets a
  short JSON verdict back (a few hundred output tokens).
- **Queries cost per use**; a typical "analyze these logs" question runs 2–6
  LLM calls with a few KB of tool results each.

Every cost lever below is a Settings-page field — changes apply immediately,
no restart needed. (They're also environment variables the agent reads on
its very first boot, but once anything has been saved, only the Settings UI
matters from then on — see [security.md](security.md).)

| Lever | Effect on cost |
| --- | --- |
| Review interval (`AGENT_REVIEW_INTERVAL`) | Biggest lever. The loop calls the LLM once per interval — `15m` cuts the default cost to a third, `1h` to a twelfth. |
| Model (`LLM_MODEL`) | Which model the calls hit. Keep the default `claude-opus-4-8` for the best analysis, or set a cheaper Claude model if its review quality is good enough for you. |
| Provider (`LLM_PROVIDER`) | `anthropic` pays per token; `ollama` runs a local model at zero API cost (needs local hardware and a tool-calling-capable model). |
| Periodic review toggle | Turns the periodic review off entirely — nothing is spent at idle; you only pay when you actually ask the assistant a question. |
