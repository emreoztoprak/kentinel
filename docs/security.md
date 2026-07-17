# Security

Read this before running the console anywhere that isn't your own machine.

## Operating mode: readonly (default) vs assisted

Kentinel runs in one of two modes, chosen at deploy time (`mode` Helm value /
`KENTINEL_MODE` env) and enforced primarily by the **server ServiceAccount's
RBAC** — not just app code:

| | `readonly` (default) | `assisted` |
| --- | --- | --- |
| Server RBAC | get/list/watch + `pods/log` | adds `update`/`patch` + `pods/exec` |
| Manifest editor / terminal | disabled (hidden; RBAC-blocked) | enabled |
| Agent remediation proposals | none | agent may propose changes |
| Can Kentinel change a resource? | **No — impossible at the cluster level** | Only via a human-approved proposal |

In readonly mode the server's ServiceAccount literally lacks write and exec
verbs, so even an app bug or a compromised LLM cannot mutate anything —
Kubernetes rejects the call. Switching modes requires a redeploy (an admin
decision), so a UI user can't grant themselves write access.

**Assisted mode never applies anything autonomously.** The LLM-facing agent
still has zero write RBAC; it can only *propose* a change (a target + a full
proposed manifest + a rationale), which a human reviews as a diff and
approves. Only then does the **server** (not the agent) apply it, via the same
guarded path the manifest editor uses, and record the outcome in an audit
trail. So even a prompt-injected agent (pod logs are attacker-influenced
input!) can at most put a proposal in the queue for a human to reject — it
cannot touch the cluster on its own. Approval means *actually reviewing the
diff*: a proposal is only as safe as the person clicking approve.

## No authentication in v1

Anyone who can reach the server port can, within the server's RBAC scope for
the current mode:

- read every resource the UI shows (including Secret manifests via the YAML view),
- in **assisted** mode: edit manifests, exec into pods, and approve agent proposals.

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
| Cluster access | get/list/watch + `pods/log` (+ `update`/`patch` + `pods/exec` in assisted mode) | get/list/watch + `pods/log` **only, always** |
| Secrets | readable (needed for the resource browser) | **no access** |
| Talks to LLM | never | yes |
| Applies changes | yes, on human approval (assisted mode) | **never** — proposes only |

The consequence holds in both modes: **nothing that touches the LLM can
mutate the cluster or read secrets.** The agent's RBAC never gains write or
exec verbs regardless of mode. Even if a model hallucinates, is
prompt-injected via pod logs, or a provider is compromised, the worst it can
do is generate a proposal a human must review and approve — it cannot apply
anything itself.

## What leaves your machine/cluster

With a cloud provider (`anthropic`, `openai`, `deepseek`, `gemini`), the
agent sends to that provider's API:

- the periodic snapshot (resource names, namespaces, counts, conditions,
  warning-event messages),
- for queries: your prompt plus tool outputs (which can include pod logs and
  resource manifests the model requested — but never Secret contents, since
  the agent has no secrets RBAC).

With `LLM_PROVIDER=ollama`, nothing leaves your infrastructure.

Separately, the **browser** (not the cluster) periodically calls GitHub's
public releases API to power the dashboard's update-check card — this is a
client-side request from whoever's viewing the UI, not an outbound call
from any pod, so it adds no new egress requirement to the cluster. It sends
nothing beyond a standard HTTPS GET; no cluster data is included.

## Secret handling

Kentinel takes the same approach as most admin-panel-style applications
(Grafana, WordPress, etc.): a deploy-time credential (env var / Secret) is
consulted **once**, to seed a brand-new install, and the application's own
database is the permanent source of truth for everything after that. There
is deliberately no live sync between the two.

- The agent's ClusterRole excludes `secrets` entirely, and this never
  changes at runtime — the agent has no code path that reads a Kubernetes
  Secret after boot.
- LLM API keys and notification webhooks can be set from the Settings page
  but are **write-only**: `GET /api/v1/agent/config` only ever reports
  set/not-set booleans, and the raw values never appear in any UI or API
  response.
- A value saved from the Settings UI is persisted by the agent to a
  `settings` table in its own SQLite file (the same one review history
  lives in) — **encrypted with AES-256-GCM**, not stored as plaintext or
  base64. The encryption key is a random 32 bytes generated on first boot
  and kept as a sibling file next to the database (mode `0600`), on the
  same PVC — nothing new to mount, no separate Kubernetes Secret to manage.
  It never appears in any Kubernetes object, so it's outside the resource
  browser's reach entirely.
- The `agent-secrets` Secret only matters **once**: on the agent's very
  first boot, when its database is genuinely empty (a fresh install, or
  the PVC was lost). From that moment on the agent's database is
  authoritative and the Secret is never read again — not on restart, not
  on a `helm upgrade`, not if you edit it directly. Replace the committed
  `REPLACE_ME` placeholders out-of-band for a scripted first install, but
  don't expect editing this Secret later to do anything; use the Settings
  UI instead. See [deployment.md](deployment.md).
  Remember the flip side of write access from the UI: since it has no
  auth, anyone who can reach it can *replace* the key or redirect the
  agent to their own Ollama host — one more reason to keep this behind
  `kubectl port-forward`.
- The UI's Secret YAML view shows base64 data as stored, for **any** Secret
  in the cluster — treat UI access as secret access when deciding who may
  reach the port. This does *not* apply to values set via the Settings UI
  (see above), which never reach a Kubernetes Secret at all.

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
- Settings saved from the UI (API keys, webhook URLs) are encrypted at rest
  in the agent's local database, never stored as plaintext or base64 — see
  Secret handling above. The database and its WAL sidecars are owner-only
  (0600), like the encryption key next to them.
- The agent reaches several **user-supplied URLs** (the Ollama host, the
  Prometheus URL, and notification webhooks). Because those requests
  originate from the agent's in-cluster network position, they're a
  server-side request forgery (SSRF) surface. Two guardrails limit it: the
  outbound dialer refuses connections to link-local / cloud-metadata
  addresses (`169.254.169.254` and friends — checked on the *resolved* IP,
  so DNS rebinding doesn't help), keeping the node's cloud IAM credentials
  off-limits even though the agent has no Kubernetes secrets access; and no
  upstream response body is ever echoed into a UI-visible error, so a
  misconfigured URL can't be turned into a read primitive. Ordinary
  cluster-private addresses stay reachable — the bundled Ollama and
  Prometheus need them.
- The dashboard's update-check card treats the GitHub API response as
  untrusted: the version shown (and embedded in the suggested `helm upgrade`
  command) is rebuilt from parsed semver components — a hostile release tag
  name can't smuggle shell text into your clipboard — and the release link
  is only rendered for a plain `https://github.com/` URL.
- The release pipeline pins all third-party actions to commit SHAs, and
  validates the release version against a strict pattern before it can
  reach any shell or artifact name — a crafted tag or workflow input fails
  the build instead of executing.

## Reporting

It's a hobby project — open an issue. Don't run it on the internet.
