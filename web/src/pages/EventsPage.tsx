import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { useNamespace } from "../context";
import { EmptyState, ErrorBox, PageTitle, Spinner, StatusBadge } from "../components/ui";
import { timeAgo } from "../util";

export default function EventsPage() {
  const { namespace } = useNamespace();
  const [type, setType] = useState("");

  const { data, error, isLoading } = useQuery({
    queryKey: ["events", namespace, type],
    queryFn: () => api.events(namespace, type),
    refetchInterval: 10_000,
  });

  return (
    <div>
      <PageTitle
        actions={
          <select className="input" value={type} onChange={(e) => setType(e.target.value)}>
            <option value="">All types</option>
            <option value="Warning">Warning</option>
            <option value="Normal">Normal</option>
          </select>
        }
      >
        Events
        {namespace && <span className="ml-2 text-sm font-normal text-slate-400">in {namespace}</span>}
      </PageTitle>

      {isLoading && <Spinner />}
      {error != null && <ErrorBox title="Could not load events" message={(error as Error).message} />}
      {data && data.length === 0 && <EmptyState message="No events found." />}

      {data && data.length > 0 && (
        <div className="card overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-200 text-left text-xs uppercase tracking-wide text-slate-400 dark:border-slate-800">
                <th className="px-4 py-2.5">Type</th>
                <th className="px-4 py-2.5">Namespace</th>
                <th className="px-4 py-2.5">Object</th>
                <th className="px-4 py-2.5">Reason</th>
                <th className="px-4 py-2.5">Message</th>
                <th className="px-4 py-2.5">Count</th>
                <th className="px-4 py-2.5">Last seen</th>
              </tr>
            </thead>
            <tbody>
              {data.map((e, i) => (
                <tr
                  key={i}
                  className="border-b border-slate-100 last:border-0 hover:bg-slate-50 dark:border-slate-800/50 dark:hover:bg-slate-800/40"
                >
                  <td className="px-4 py-2">
                    <StatusBadge status={e.type} />
                  </td>
                  <td className="px-4 py-2 text-slate-500">{e.namespace || "-"}</td>
                  <td className="px-4 py-2 font-medium">{e.object}</td>
                  <td className="px-4 py-2">{e.reason}</td>
                  <td className="max-w-md px-4 py-2 text-slate-600 dark:text-slate-300">
                    <span className="line-clamp-2" title={e.message}>
                      {e.message}
                    </span>
                  </td>
                  <td className="px-4 py-2 text-slate-500">{e.count}</td>
                  <td className="px-4 py-2 text-slate-500">{timeAgo(e.lastSeen)} ago</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
