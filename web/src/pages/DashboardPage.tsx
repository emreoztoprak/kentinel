import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import AiPanel from "../components/AiPanel";
import ProposalsPanel from "../components/ProposalsPanel";
import UpdateStatus from "../components/UpdateStatus";
import { ErrorBox, PageTitle, Spinner, StatusBadge } from "../components/ui";
import { timeAgo } from "../util";

export default function DashboardPage() {
  const { data, error, isLoading } = useQuery({
    queryKey: ["overview"],
    queryFn: api.overview,
    refetchInterval: 10_000,
  });
  const { data: settings } = useQuery({ queryKey: ["server-settings"], queryFn: api.serverSettings });
  const assisted = settings?.mode === "assisted";

  return (
    <div>
      <PageTitle>Dashboard</PageTitle>

      <UpdateStatus />

      {assisted && <ProposalsPanel />}

      <div className="mb-6">
        <AiPanel />
      </div>

      {isLoading && <Spinner label="Loading cluster overview..." />}
      {error != null && (
        <ErrorBox title="Could not load cluster overview" message={(error as Error).message} />
      )}

      {data && (
        <>
          <div className="mb-6 grid grid-cols-2 gap-4 lg:grid-cols-4">
            <StatCard
              title="Nodes"
              value={`${data.nodes.ready}/${data.nodes.total}`}
              sub="ready"
              to="/resources/nodes"
              alert={data.nodes.ready < data.nodes.total}
            />
            <StatCard
              title="Pods"
              value={`${data.pods.running}/${data.pods.total}`}
              sub="running"
              to="/resources/pods"
              alert={data.pods.failed > 0}
            />
            <StatCard title="Namespaces" value={String(data.namespaces)} sub="total" />
            <StatCard
              title="Deployments"
              value={`${data.deployments.available}/${data.deployments.total}`}
              sub="available"
              to="/resources/deployments"
              alert={data.deployments.available < data.deployments.total}
            />
          </div>

          <div className="grid gap-6 lg:grid-cols-2">
            <div className="card p-4">
              <h2 className="mb-3 font-semibold">Pod status</h2>
              <PodBreakdown pods={data.pods} />
            </div>

            <div className="card p-4">
              <div className="mb-3 flex items-center justify-between">
                <h2 className="font-semibold">Recent warnings</h2>
                <Link to="/events" className="text-sm text-indigo-500 hover:underline">
                  All events →
                </Link>
              </div>
              {data.warnings.length === 0 ? (
                <p className="text-sm text-slate-500">No warning events. 🎉</p>
              ) : (
                <ul className="space-y-2">
                  {data.warnings.slice(0, 6).map((w, i) => (
                    <li key={i} className="text-sm">
                      <div className="flex items-center gap-2">
                        <StatusBadge status={w.type} />
                        <span className="font-medium">{w.reason}</span>
                        <span className="text-xs text-slate-400">
                          {w.namespace}/{w.object} · {timeAgo(w.lastSeen)} ago
                        </span>
                      </div>
                      <p className="truncate text-slate-600 dark:text-slate-300" title={w.message}>
                        {w.message}
                      </p>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </div>
        </>
      )}
    </div>
  );
}

function StatCard({
  title,
  value,
  sub,
  to,
  alert,
}: {
  title: string;
  value: string;
  sub: string;
  to?: string;
  alert?: boolean;
}) {
  const body = (
    <div className={`card p-4 ${alert ? "border-amber-400 dark:border-amber-700" : ""}`}>
      <div className="text-sm text-slate-500 dark:text-slate-400">{title}</div>
      <div className="mt-1 text-2xl font-semibold">
        {value} {alert && <span title="Needs attention">⚠️</span>}
      </div>
      <div className="text-xs text-slate-400">{sub}</div>
    </div>
  );
  return to ? <Link to={to}>{body}</Link> : body;
}

function PodBreakdown({
  pods,
}: {
  pods: { running: number; pending: number; succeeded: number; failed: number; unknown: number; total: number };
}) {
  const segments = [
    { label: "Running", count: pods.running, color: "bg-emerald-500" },
    { label: "Pending", count: pods.pending, color: "bg-amber-400" },
    { label: "Succeeded", count: pods.succeeded, color: "bg-sky-400" },
    { label: "Failed", count: pods.failed, color: "bg-red-500" },
    { label: "Unknown", count: pods.unknown, color: "bg-slate-400" },
  ].filter((s) => s.count > 0);

  if (pods.total === 0) return <p className="text-sm text-slate-500">No pods in the cluster.</p>;

  return (
    <div>
      <div className="flex h-3 w-full overflow-hidden rounded-full bg-slate-100 dark:bg-slate-800">
        {segments.map((s) => (
          <div
            key={s.label}
            className={s.color}
            style={{ width: `${(s.count / pods.total) * 100}%` }}
            title={`${s.label}: ${s.count}`}
          />
        ))}
      </div>
      <div className="mt-3 flex flex-wrap gap-4 text-sm">
        {segments.map((s) => (
          <span key={s.label} className="flex items-center gap-1.5">
            <span className={`h-2.5 w-2.5 rounded-full ${s.color}`} />
            {s.label} <span className="font-medium">{s.count}</span>
          </span>
        ))}
      </div>
    </div>
  );
}
