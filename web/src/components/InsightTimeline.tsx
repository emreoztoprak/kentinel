import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, type InsightStatus } from "../api/client";

const STATUS_COLORS: Record<InsightStatus, string> = {
  healthy: "bg-emerald-500",
  warning: "bg-amber-400",
  critical: "bg-red-500",
  error: "bg-slate-300 dark:bg-slate-600",
};

// InsightTimeline is the dashboard trend strip: one segment per review over
// the last 24h, colored by status.
export default function InsightTimeline() {
  const { data } = useQuery({
    queryKey: ["agent-timeline"],
    queryFn: () => api.agentTimeline(24),
    refetchInterval: 60_000,
    retry: false,
  });

  if (!data || data.points.length < 2) return null; // nothing worth drawing yet

  return (
    <div className="mt-3 border-t border-slate-200 pt-3 dark:border-slate-800">
      <div className="mb-1.5 flex items-center justify-between text-xs text-slate-400">
        <span>Last 24h · {data.points.length} reviews</span>
        <Link to="/insights" className="text-indigo-500 hover:underline">
          Full history →
        </Link>
      </div>
      <div className="flex h-2.5 w-full gap-px overflow-hidden rounded-full">
        {data.points.map((p, i) => (
          <div
            key={i}
            className={`min-w-[2px] flex-1 ${STATUS_COLORS[p.status] ?? STATUS_COLORS.error}`}
            title={`${new Date(p.t).toLocaleString()} — ${p.status}`}
          />
        ))}
      </div>
      {!data.persistent && (
        <p className="mt-1.5 text-xs text-slate-400">
          History is in-memory only (lost on restart) — set INSIGHT_DB_PATH to persist.
        </p>
      )}
    </div>
  );
}
