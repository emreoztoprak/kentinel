// Typed client for the backend API. All calls go through apiFetch so error
// envelopes ({error, message}) surface as ApiError with a readable message.

export class ApiError extends Error {
  status: number;
  code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, init);
  if (!res.ok) {
    let code = "error";
    let message = `${res.status} ${res.statusText}`;
    try {
      const body = await res.json();
      if (body?.message) {
        code = body.error ?? code;
        message = body.message;
      }
    } catch {
      // non-JSON error body; keep the status text
    }
    throw new ApiError(res.status, code, message);
  }
  return res.json() as Promise<T>;
}

// ---- Types mirrored from the Go backend ----

export interface Overview {
  nodes: { total: number; ready: number };
  pods: {
    total: number;
    running: number;
    pending: number;
    succeeded: number;
    failed: number;
    unknown: number;
  };
  namespaces: number;
  deployments: { total: number; available: number };
  warnings: EventSummary[];
  collectedAt: string;
}

export interface EventSummary {
  namespace: string;
  type: string;
  reason: string;
  object: string;
  message: string;
  count: number;
  lastSeen: string;
}

export interface KindInfo {
  kind: string;
  displayName: string;
  namespaced: boolean;
}

export interface ResourceSummary {
  name: string;
  namespace?: string;
  createdAt: string;
  extra?: Record<string, string>;
}

export interface ResourceDetail {
  name: string;
  namespace?: string;
  kind: string;
  createdAt: string;
  labels?: Record<string, string>;
  object: Record<string, unknown>;
  yaml: string;
}

export type InsightStatus = "healthy" | "warning" | "critical" | "error";

export interface Finding {
  severity: string;
  resource: string;
  title: string;
  detail: string;
  recommendation?: string;
}

export interface Insight {
  status: InsightStatus;
  summary: string;
  findings: Finding[] | null;
  createdAt: string;
  durationMs: number;
  provider: string;
  model: string;
  reviewError?: string;
}

export interface AgentStatus {
  provider: string;
  model: string;
  latest: Insight | null;
  historyPersistent: boolean;
}

export interface TimelinePoint {
  t: string;
  status: InsightStatus;
}

export interface QueryEvent {
  // "proposal": content is JSON of a Proposal (assisted mode) — rendered as
  // an inline approval card.
  type: "text" | "tool" | "done" | "error" | "proposal";
  content: string;
}

export interface AgentConfig {
  provider: string;
  model: string;
  ollamaHost: string;
  apiKeysSet: Record<string, boolean>; // provider -> key configured?
  reviewInterval: string;
  monitorEnabled: boolean;
  notificationsEnabled: boolean;
  discordWebhookSet: boolean;
  slackWebhookSet: boolean;
  teamsWebhookSet: boolean;
  notifyMinSeverity: string;
  prometheusUrl: string;
  insightRetentionDays: number;
  // Whether this state survives a pod restart (a working SQLite file +
  // encryption key in the agent). false in Docker mode without
  // INSIGHT_DB_PATH — the change still applies live either way.
  persistent: boolean;
}

export interface AgentConfigUpdate {
  provider: string;
  model: string;
  ollamaHost: string;
  apiKey?: string; // write-only, applies to the selected provider; empty = keep existing
  reviewInterval: string;
  monitorEnabled: boolean;
  notificationsEnabled: boolean;
  discordWebhookUrl?: string; // write-only; empty = keep existing
  slackWebhookUrl?: string; // write-only; empty = keep existing
  teamsWebhookUrl?: string; // write-only; empty = keep existing
  notifyMinSeverity: string;
  prometheusUrl: string; // empty disables metrics
  insightRetentionDays: number; // 0 = leave unchanged
}

export interface ServerSettings {
  agentUrl: string;
  staticDir: string;
  inCluster: boolean;
  namespace: string;
  version: string; // "dev" outside a released build
  mode: "readonly" | "assisted";
}

export interface UsageSource {
  source: string; // "review" | "query"
  calls: number;
  inputTokens: number;
  outputTokens: number;
  costUsd: number;
}

export interface UsageSummary {
  days: number;
  provider: string;
  model: string;
  hasPricing: boolean; // false for Ollama / unpriced models — show tokens only
  inputTokens: number;
  outputTokens: number;
  costUsd: number;
  bySource: UsageSource[];
}

export interface Proposal {
  id: string;
  createdAt: string;
  status: "pending" | "rejected" | "applied" | "failed";
  kind: string;
  namespace: string;
  name: string;
  rationale: string;
  currentYaml: string;
  proposedYaml: string;
  decidedAt?: string;
  error?: string;
}

// ---- API calls ----

export const api = {
  overview: () => apiFetch<Overview>("/api/v1/overview"),
  namespaces: () =>
    apiFetch<{ namespaces: string[] }>("/api/v1/namespaces").then((r) => r.namespaces),
  kinds: () => apiFetch<{ kinds: KindInfo[] }>("/api/v1/kinds").then((r) => r.kinds),
  events: (namespace?: string, type?: string) => {
    const params = new URLSearchParams();
    if (namespace) params.set("namespace", namespace);
    if (type) params.set("type", type);
    return apiFetch<{ events: EventSummary[] }>(`/api/v1/events?${params}`).then((r) => r.events);
  },
  listResources: (kind: string, namespace?: string) => {
    const params = new URLSearchParams();
    if (namespace) params.set("namespace", namespace);
    return apiFetch<{ items: ResourceSummary[] }>(`/api/v1/resources/${kind}/?${params}`).then(
      (r) => r.items ?? [],
    );
  },
  getResource: (kind: string, namespace: string, name: string) =>
    apiFetch<ResourceDetail>(
      `/api/v1/resources/${kind}/${encodeURIComponent(namespace || "-")}/${encodeURIComponent(name)}`,
    ),
  updateResource: (kind: string, namespace: string, name: string, yaml: string) =>
    apiFetch<ResourceDetail>(
      `/api/v1/resources/${kind}/${encodeURIComponent(namespace || "-")}/${encodeURIComponent(name)}`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ yaml }),
      },
    ),
  podContainers: (namespace: string, name: string) =>
    apiFetch<{ containers: string[] }>(
      `/api/v1/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/containers`,
    ).then((r) => r.containers),
  agentStatus: () => apiFetch<AgentStatus>("/api/v1/agent/status"),
  agentInsights: (opts?: { limit?: number; status?: string; since?: string }) => {
    const params = new URLSearchParams();
    if (opts?.limit) params.set("limit", String(opts.limit));
    if (opts?.status) params.set("status", opts.status);
    if (opts?.since) params.set("since", opts.since);
    return apiFetch<{ insights: Insight[] | null; persistent: boolean }>(
      `/api/v1/agent/insights?${params}`,
    );
  },
  agentTimeline: (hours = 24) =>
    apiFetch<{ points: TimelinePoint[]; hours: number; persistent: boolean }>(
      `/api/v1/agent/insights/timeline?hours=${hours}`,
    ),
  agentConfig: () => apiFetch<AgentConfig>("/api/v1/agent/config"),
  updateAgentConfig: (update: AgentConfigUpdate) =>
    apiFetch<AgentConfig>("/api/v1/agent/config", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(update),
    }),
  serverSettings: () => apiFetch<ServerSettings>("/api/v1/settings"),
  usage: (days = 30) => apiFetch<UsageSummary>(`/api/v1/agent/usage?days=${days}`),

  // Remediation proposals (assisted mode). List/reject proxy to the agent;
  // apply goes to the SERVER (it holds the write RBAC).
  proposals: (pendingOnly = false) =>
    apiFetch<{ proposals: Proposal[] }>(
      `/api/v1/agent/proposals${pendingOnly ? "?pending=true" : ""}`,
    ),
  applyProposal: (id: string) =>
    apiFetch<{ status: string; id: string }>(`/api/v1/proposals/${id}/apply`, { method: "POST" }),
  rejectProposal: (id: string) =>
    apiFetch<{ status: string }>(`/api/v1/agent/proposals/${id}/reject`, { method: "POST" }),

  testNotification: () =>
    apiFetch<{ status: string }>("/api/v1/agent/notifications/test", { method: "POST" }),
  metricsHealth: () => apiFetch<{ status: string }>("/api/v1/agent/metrics/health"),
  agentModels: (provider: string, host?: string) => {
    const params = new URLSearchParams({ provider });
    if (host) params.set("host", host);
    return apiFetch<{ provider: string; models: string[]; default: string; error?: string }>(
      `/api/v1/agent/models?${params}`,
    );
  },
};

export interface QueryTurn {
  role: "user" | "assistant";
  text: string;
}

// agentQuery streams SSE events from the agent, sending the full conversation
// so the assistant keeps context across turns. Returns an abort function.
export function agentQuery(
  messages: QueryTurn[],
  onEvent: (ev: QueryEvent) => void,
  onError: (message: string) => void,
): () => void {
  const controller = new AbortController();

  (async () => {
    try {
      const res = await fetch("/api/v1/agent/query", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ messages }),
        signal: controller.signal,
      });
      if (!res.ok || !res.body) {
        let message = `agent request failed (${res.status})`;
        try {
          const body = await res.json();
          if (body?.message) message = body.message;
        } catch {
          /* keep default */
        }
        onError(message);
        return;
      }

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        const frames = buffer.split("\n\n");
        buffer = frames.pop() ?? "";
        for (const frame of frames) {
          for (const line of frame.split("\n")) {
            if (line.startsWith("data: ")) {
              try {
                onEvent(JSON.parse(line.slice(6)) as QueryEvent);
              } catch {
                /* skip malformed frame */
              }
            }
          }
        }
      }
    } catch (err) {
      if (!controller.signal.aborted) {
        onError(err instanceof Error ? err.message : "connection to agent lost");
      }
    }
  })();

  return () => controller.abort();
}
