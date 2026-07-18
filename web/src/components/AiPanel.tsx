import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, type Insight } from "../api/client";
import { StatusBadge } from "./ui";
import InsightTimeline from "./InsightTimeline";
import { timeAgo } from "../util";

const STATUS_STYLES: Record<string, string> = {
  healthy: "border-emerald-300 dark:border-emerald-800",
  warning: "border-amber-400 dark:border-amber-700",
  critical: "border-red-400 dark:border-red-700",
  error: "border-slate-300 dark:border-slate-700",
};

// prettyInterval maps Go's "30m0s" to "30m" for display.
function prettyInterval(d: string): string {
  return d.replace(/0s$/, "").replace(/0m$/, "") || d;
}

// AiPanel shows the latest periodic cluster review on the dashboard,
// including a prominent banner when something is wrong.
export default function AiPanel() {
  const { data, error, isLoading } = useQuery({
    queryKey: ["agent-status"],
    queryFn: api.agentStatus,
    refetchInterval: 15_000,
    retry: false,
  });
  const { data: config } = useQuery({ queryKey: ["agent-config"], queryFn: api.agentConfig, retry: false });

  return (
    <div
      className={`card border-2 p-4 ${STATUS_STYLES[data?.latest?.status ?? "error"] ?? STATUS_STYLES.error}`}
    >
      <div className="mb-2 flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <span className="text-base font-semibold">🤖 AI Cluster Review</span>
          {data?.latest && <StatusBadge status={data.latest.status} />}
        </div>
        <div className="flex items-center gap-2">
          {config &&
            (config.monitorEnabled ? (
              <span
                className="rounded-full bg-emerald-100 px-2 py-0.5 text-xs font-medium text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300"
                title="The agent reviews the cluster on this schedule."
              >
                🔄 Every {prettyInterval(config.reviewInterval)}
              </span>
            ) : (
              <span
                className="rounded-full bg-slate-100 px-2 py-0.5 text-xs font-medium text-slate-600 dark:bg-slate-800 dark:text-slate-300"
                title="Turn periodic review on in Settings. The assistant still works on demand."
              >
                ⏸ Periodic review off
              </span>
            ))}
          {data && (
            <span className="text-xs text-slate-400">
              {data.provider} / {data.model}
            </span>
          )}
        </div>
      </div>

      {config && !config.monitorEnabled && (
        <p className="mb-2 text-sm text-slate-500">
          Periodic review is off — no automatic checks are running. Any review shown below is the
          last one before it was disabled. Enable it in{" "}
          <Link to="/settings" className="text-indigo-500 hover:underline">
            Settings
          </Link>
          .
        </p>
      )}

      {isLoading && <p className="text-sm text-slate-500">Checking agent status…</p>}

      {error != null && (
        <p className="text-sm text-slate-500">
          The AI agent is not reachable. Check that the agent service is running — see{" "}
          <code className="rounded bg-slate-100 px-1 dark:bg-slate-800">docs/deployment.md</code>.
        </p>
      )}

      {data && !data.latest && (
        <p className="text-sm text-slate-500">
          The agent is running; the first cluster review has not completed yet.
        </p>
      )}

      {data?.latest && <InsightBody insight={data.latest} />}
      <InsightTimeline />
    </div>
  );
}

function InsightBody({ insight }: { insight: Insight }) {
  const findings = insight.findings ?? [];
  return (
    <div>
      {(insight.status === "warning" || insight.status === "critical") && (
        <div
          className={`mb-3 rounded-lg px-3 py-2 text-sm font-medium ${
            insight.status === "critical"
              ? "bg-red-100 text-red-800 dark:bg-red-950 dark:text-red-200"
              : "bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-200"
          }`}
        >
          {insight.status === "critical" ? "⛔" : "⚠️"} Attention needed — see findings below.
        </div>
      )}

      <p className="text-sm">{insight.summary}</p>
      {insight.reviewError && (
        <p className="mt-1 break-words text-xs text-red-500">{insight.reviewError}</p>
      )}

      {findings.length > 0 && (
        <ul className="mt-3 space-y-2">
          {findings.map((f, i) => (
            <li
              key={i}
              className="rounded-lg bg-slate-50 px-3 py-2 text-sm dark:bg-slate-800/60"
            >
              <div className="flex items-center gap-2">
                <StatusBadge status={f.severity} />
                <span className="font-medium">{f.title}</span>
                <span className="text-xs text-slate-400">{f.resource}</span>
              </div>
              <p className="mt-1 text-slate-600 dark:text-slate-300">{f.detail}</p>
              {f.recommendation && (
                <p className="mt-1 text-xs text-slate-500">
                  <span className="font-medium">Recommendation:</span> {f.recommendation}
                </p>
              )}
            </li>
          ))}
        </ul>
      )}

      <div className="mt-3 flex items-center justify-between text-xs text-slate-400">
        <span>
          Reviewed {timeAgo(insight.createdAt)} ago · took{" "}
          {(insight.durationMs / 1000).toFixed(1)}s
        </span>
        <Link to="/assistant" className="text-indigo-500 hover:underline">
          Ask the assistant →
        </Link>
      </div>
    </div>
  );
}
