import { useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { useNamespace } from "../context";
import { EmptyState, ErrorBox, PageTitle, Spinner, StatusBadge } from "../components/ui";
import { timeAgo } from "../util";

// Column keys rendered per kind, in order. Anything not listed falls back to
// name/namespace/age only.
const KIND_COLUMNS: Record<string, string[]> = {
  pods: ["status", "ready", "restarts", "node", "ip"],
  deployments: ["ready", "updated", "available"],
  statefulsets: ["ready", "updated", "available"],
  daemonsets: ["ready"],
  services: ["type", "clusterIP", "ports"],
  configmaps: ["keys"],
  secrets: ["type", "keys"],
  nodes: ["status", "roles", "version"],
  jobs: ["completions", "failed"],
  cronjobs: ["schedule", "suspend", "lastRun"],
  persistentvolumeclaims: ["status", "capacity", "class"],
  ingresses: [],
};

const STATUS_COLUMNS = new Set(["status", "type"]);

export default function ResourceListPage() {
  const { kind = "" } = useParams();
  const { namespace } = useNamespace();
  const [search, setSearch] = useState("");

  const { data, error, isLoading } = useQuery({
    queryKey: ["resources", kind, namespace],
    queryFn: () => api.listResources(kind, namespace),
    refetchInterval: 10_000,
  });

  const columns = KIND_COLUMNS[kind] ?? [];

  const items = useMemo(() => {
    if (!data) return [];
    const q = search.trim().toLowerCase();
    if (!q) return data;
    return data.filter(
      (r) => r.name.toLowerCase().includes(q) || (r.namespace ?? "").toLowerCase().includes(q),
    );
  }, [data, search]);

  return (
    <div>
      <PageTitle
        actions={
          <input
            className="input w-64"
            placeholder="Search by name..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        }
      >
        {kind}
        {namespace && <span className="ml-2 text-sm font-normal text-slate-400">in {namespace}</span>}
      </PageTitle>

      {isLoading && <Spinner />}
      {error != null && <ErrorBox title={`Could not list ${kind}`} message={(error as Error).message} />}

      {data && items.length === 0 && <EmptyState message={`No ${kind} found.`} />}

      {items.length > 0 && (
        <div className="card overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-200 text-left text-xs uppercase tracking-wide text-slate-400 dark:border-slate-800">
                <th className="px-4 py-2.5">Name</th>
                <th className="px-4 py-2.5">Namespace</th>
                {columns.map((c) => (
                  <th key={c} className="px-4 py-2.5">
                    {c}
                  </th>
                ))}
                <th className="px-4 py-2.5">Age</th>
              </tr>
            </thead>
            <tbody>
              {items.map((r) => (
                <tr
                  key={`${r.namespace}/${r.name}`}
                  className="border-b border-slate-100 last:border-0 hover:bg-slate-50 dark:border-slate-800/50 dark:hover:bg-slate-800/40"
                >
                  <td className="px-4 py-2">
                    <Link
                      className="font-medium text-indigo-600 hover:underline dark:text-indigo-400"
                      to={`/resources/${kind}/${r.namespace || "-"}/${r.name}`}
                    >
                      {r.name}
                    </Link>
                  </td>
                  <td className="px-4 py-2 text-slate-500">{r.namespace ?? "-"}</td>
                  {columns.map((c) => (
                    <td key={c} className="px-4 py-2">
                      {STATUS_COLUMNS.has(c) && r.extra?.[c] ? (
                        <StatusBadge status={r.extra[c]} />
                      ) : (
                        <span className="text-slate-600 dark:text-slate-300">
                          {r.extra?.[c] ?? "-"}
                        </span>
                      )}
                    </td>
                  ))}
                  <td className="px-4 py-2 text-slate-500">{timeAgo(r.createdAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
