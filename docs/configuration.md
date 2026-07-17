# Configuration

All configuration is via environment variables. Everything has a default —
both binaries start with zero config against a local kubeconfig.

## Settings UI

Most agent parameters can also be changed at runtime on the **Settings page**
(sidebar → System → Settings): LLM provider, model, Ollama host, Anthropic
API key (write-only — never displayed), review interval, and the periodic
review on/off switch.

How it works:

- Changes **apply immediately** in the running agent — no pod restart, the
  insight history is kept, and a fresh review runs right away.
- The agent also persists the change to a `settings` table in its own
  SQLite file (the same one insight history uses), **encrypted** — not
  plaintext or base64. This works identically in Docker and k8s mode, as
  long as `INSIGHT_DB_PATH` points at a persistent volume (the default in
  both `docker-compose.yml` and the Helm chart / raw manifests). No
  volume, no key file, no persistence — the UI tells you which case you're
  in (`persistent: true/false`) after every save.
- Once anything has ever been saved, it's permanent: the deployment's env
  vars / Secret only matter on the agent's very first boot (a genuinely
  empty database). A later `helm upgrade --set`, `kubectl edit`, or
  manifest re-apply has **no effect** — the Settings UI is the only way to
  change something after that first boot. See [security.md](security.md).

Server parameters (port, agent URL, RBAC) are deployment-level and remain
read-only in the UI — change them in the manifests / compose file.

## Server (`cmd/server`)

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `8080` | HTTP listen port |
| `AGENT_URL` | `http://localhost:8090` | Base URL of the agent service; `/api/v1/agent/*` is proxied here |
| `KUBECONFIG_PATH` | *(auto)* | Explicit kubeconfig path. When unset: in-cluster config if running in a pod, else standard kubeconfig rules (`KUBECONFIG` env, `~/.kube/config`) |
| `STATIC_DIR` | *(auto)* | Directory of the built SPA. When unset: `web/dist` if present, else API-only (dev mode) |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | `text` | `text` or `json` |

## Agent (`cmd/agent`)

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `8090` | HTTP listen port |
| `LLM_PROVIDER` | `ollama` | `ollama` (local, free) or a cloud provider: `anthropic`, `openai`, `deepseek`, `gemini` |
| `LLM_MODEL` | *(per provider)* | Model ID. Defaults: `qwen3:0.6b` (ollama), `claude-opus-4-8` (anthropic), `gpt-5.1` (openai), `deepseek-chat` (deepseek), `gemini-2.5-flash` (gemini). The k8s manifests set `qwen3:0.6b` to fit small clusters |
| `ANTHROPIC_API_KEY` | — | Required when `LLM_PROVIDER=anthropic` |
| `OPENAI_API_KEY` | — | Required when `LLM_PROVIDER=openai` |
| `DEEPSEEK_API_KEY` | — | Required when `LLM_PROVIDER=deepseek` |
| `GEMINI_API_KEY` | — | Required when `LLM_PROVIDER=gemini` |
| `OLLAMA_HOST` | `http://localhost:11434` | Ollama base URL (ollama provider only) |
| `AGENT_REVIEW_INTERVAL` | `5m` | How often the monitor reviews the cluster (Go duration, min `30s`) |
| `AGENT_MONITOR_ENABLED` | `true` | Set `false` to disable the periodic review loop (queries still work) |
| `NOTIFICATIONS_ENABLED` | `false` | Send Discord alerts when the cluster status *changes* (healthy→warning→critical and recoveries). Never per-review |
| `DISCORD_WEBHOOK_URL` | — | Discord webhook to post alerts to. Write-only in the API/UI |
| `SLACK_WEBHOOK_URL` | — | Slack incoming webhook (`https://hooks.slack.com/...`). Write-only in the API/UI |
| `TEAMS_WEBHOOK_URL` | — | Microsoft Teams Workflows webhook (Adaptive Card payload). Write-only in the API/UI |
| `NOTIFY_MIN_SEVERITY` | `warning` | `warning` = alert on any degradation; `critical` = only page for outages (recoveries from qualifying states still notify) |
| `INSIGHT_DB_PATH` | *(empty)* | SQLite file for persistent review history (e.g. `/data/insights.db`). Empty = in-memory only, history lost on restart. The k8s manifests set this and mount a PVC |
| `INSIGHT_RETENTION_DAYS` | `90` | Reviews older than this are pruned from the database |
| `PROMETHEUS_URL` | *(empty)* | Prometheus base URL for the agent's metrics tools (usage, throttling). The k8s manifests point this at the bundled Prometheus; set your own to reuse an existing one. Empty = metrics tools disabled |
| `KUBECONFIG_PATH` | *(auto)* | Same resolution as the server |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `text` | Same as the server |

## LLM provider setup

### Ollama (local models — the default)

1. Get an Ollama endpoint:
   - k8s mode: `deploy/k8s/05-ollama.yaml` runs it in-cluster and auto-pulls
     `qwen3:0.6b` on first boot — nothing to do.
   - Docker mode: `docker compose --profile ollama up`, then
     `docker compose exec ollama ollama pull qwen3:0.6b` once.
   - Local dev: install Ollama (<https://ollama.com>) and `ollama pull qwen3:0.6b`.
2. The model must be **tool-calling-capable** — the query engine requires
   function calling. Known-good: `qwen3` / `qwen3:0.6b`, `llama3.1`,
   `mistral-nemo`.
3. Override with `LLM_MODEL` and `OLLAMA_HOST` as needed.

Notes:

- Small local models (like the 0.6b default in the k8s manifests) are
  noticeably weaker at the structured review and multi-step tool use. If
  reviews come back as `status: "error"` with a parse failure or queries
  wander, move up to `qwen3` (8B) or switch to anthropic.
- First request after a pull loads the model into memory and can take a
  minute; the agent tolerates up to 5 minutes per LLM call.

### Cloud providers (Anthropic, OpenAI, DeepSeek, Gemini)

All four work the same way — pick the provider, provide its API key
(easiest: Settings page, which stores it write-only and persists it
encrypted to the agent's own database), optionally pick a model from the
dropdown:

| Provider | Get a key at | Default model | Notes |
| --- | --- | --- | --- |
| `anthropic` | platform.claude.com | `claude-opus-4-8` | Strongest agentic tool use in our testing |
| `openai` | platform.openai.com | `gpt-5.1` | |
| `deepseek` | platform.deepseek.com | `deepseek-chat` | Very low cost; `deepseek-reasoner` for harder analysis |
| `gemini` | aistudio.google.com | `gemini-2.5-flash` | Served via Google's OpenAI-compatible endpoint |

The model dropdowns show a curated list; any valid model ID for that
provider also works (the lists are a convenience, not an allowlist). The
agent requires models that support **function calling** — all listed
defaults do.

## Cost control (anthropic)

The monitor loop calls the LLM once per interval, 24/7. With the default 5m
interval that is ~8,600 calls/month; each call sends a compact snapshot
(typically 1–3K tokens) and receives a short JSON verdict. Levers, in order of
impact:

1. `AGENT_REVIEW_INTERVAL=15m` (or more) — proportional cost reduction
2. `AGENT_MONITOR_ENABLED=false` — on-demand queries only
3. `LLM_MODEL` — a smaller/cheaper Claude model for the loop
