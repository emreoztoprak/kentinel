import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, type Insight } from "../api/client";
import { EmptyState, ErrorBox, PageTitle, Spinner, StatusBadge } from "../components/ui";
import { timeAgo } from "../util";

// InsightsPage is the AI review history: every past cluster review with its
// findings, filterable by status.
export default function InsightsPage() {
  const [status, setStatus] = useState("");

  const { data, error, isLoading } = useQuery({
    queryKey: ["agent-insights", status],
    queryFn: () => api.agentInsights({ limit: 100, status: status || undefined }),
    refetchInterval: 30_000,
    retry: false,
  });

  const insights = data?.insights ?? [];

  return (
    <div className="mx-auto max-w-3xl">
      <PageTitle
        actions={
          <select className="input" value={status} onChange={(e) => setStatus(e.target.value)}>
            <option value="">All statuses</option>
            <option value="healthy">healthy</option>
            <option value="warning">warning</option>
            <option value="critical">critical</option>
            <option value="error">error</option>
          </select>
        }
      >
        AI Review History
      </PageTitle>

      {data && !data.persistent && (
        <div className="mb-4 rounded-lg border border-amber-300 bg-amber-50 px-4 py-2 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200">
          History is in-memory only and resets when the agent restarts. In k8s mode the default
          manifests persist it to a PVC — see Documentation → AI Agent → Insight history.
        </div>
      )}

      {isLoading && <Spinner label="Loading review history..." />}
      {error != null && (
        <ErrorBox
          title="Could not load history"
          message={(error as Error).message + " — is the agent service running?"}
        />
      )}
      {data && insights.length === 0 && <EmptyState message="No reviews recorded yet." />}

      <div className="space-y-3">
        {insights.map((insight, i) => (
          <InsightCard key={`${insight.createdAt}-${i}`} insight={insight} />
        ))}
      </div>
    </div>
  );
}

function InsightCard({ insight }: { insight: Insight }) {
  const findings = insight.findings ?? [];
  return (
    <details className="card group px-4 py-3">
      <summary className="flex cursor-pointer list-none items-center gap-3 [&::-webkit-details-marker]:hidden">
        <StatusBadge status={insight.status} />
        <span className="min-w-0 flex-1 truncate text-sm text-slate-700 dark:text-slate-300">
          {insight.summary}
        </span>
        <span className="shrink-0 text-xs text-slate-400">
          {findings.length > 0 && `${findings.length} finding${findings.length > 1 ? "s" : ""} · `}
          {timeAgo(insight.createdAt)} ago
        </span>
        <span className="shrink-0 text-slate-400 transition-transform group-open:rotate-90">›</span>
      </summary>

      <div className="mt-3 border-t border-slate-100 pt-3 dark:border-slate-800">
        <p className="text-sm text-slate-700 dark:text-slate-300">{insight.summary}</p>
        {insight.reviewError && (
          <p className="mt-1 break-words text-xs text-red-500">{insight.reviewError}</p>
        )}

        {findings.length > 0 && (
          <ul className="mt-3 space-y-2">
            {findings.map((f, i) => (
              <li key={i} className="rounded-lg bg-slate-50 px-3 py-2 text-sm dark:bg-slate-800/60">
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

        <p className="mt-3 text-xs text-slate-400">
          {new Date(insight.createdAt).toLocaleString()} · {insight.provider}/{insight.model} ·
          took {(insight.durationMs / 1000).toFixed(1)}s
        </p>
      </div>
    </details>
  );
}
