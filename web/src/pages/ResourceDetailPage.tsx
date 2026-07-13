import { lazy, Suspense, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { ErrorBox, PageTitle, Spinner, StatusBadge } from "../components/ui";

// Monaco is heavy — load the editor chunk only when the YAML tab is opened.
const YamlEditor = lazy(() => import("../components/YamlEditor"));
import LogsView from "../components/LogsView";
import TerminalView from "../components/TerminalView";
import { timeAgo } from "../util";

export default function ResourceDetailPage() {
  const { kind = "", namespace: nsParam = "-", name = "" } = useParams();
  const namespace = nsParam === "-" ? "" : nsParam;
  const isPod = kind === "pods";
  const navigate = useNavigate();

  const tabs = ["Overview", "YAML", "Events", ...(isPod ? ["Logs", "Terminal"] : [])];
  const [tab, setTab] = useState("Overview");

  const { data, error, isLoading } = useQuery({
    queryKey: ["resource", kind, namespace, name],
    queryFn: () => api.getResource(kind, namespace, name),
    refetchInterval: tab === "Overview" ? 10_000 : false,
  });

  const { data: events } = useQuery({
    queryKey: ["events", namespace],
    queryFn: () => api.events(namespace),
    enabled: tab === "Events",
    refetchInterval: 10_000,
  });

  const dark = document.documentElement.classList.contains("dark");
  const resourceEvents = (events ?? []).filter((e) => e.object.endsWith(`/${name}`));

  const askAi = () => {
    const prompt = `Analyze the ${kind.replace(/s$/, "")} "${name}"${
      namespace ? ` in namespace "${namespace}"` : ""
    }: check its manifest, related events${isPod ? " and recent logs" : ""}, and tell me if anything is wrong.`;
    navigate("/assistant", { state: { prompt } });
  };

  return (
    <div>
      <PageTitle
        actions={
          <button className="btn-primary" onClick={askAi}>
            🤖 Ask AI about this resource
          </button>
        }
      >
        <span className="text-slate-400">
          <Link to={`/resources/${kind}`} className="hover:underline">
            {kind}
          </Link>{" "}
          /
        </span>{" "}
        {name}
        {namespace && <span className="ml-2 text-sm font-normal text-slate-400">in {namespace}</span>}
      </PageTitle>

      <div className="mb-4 flex gap-1 border-b border-slate-200 dark:border-slate-800">
        {tabs.map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`px-4 py-2 text-sm ${
              tab === t
                ? "border-b-2 border-indigo-600 font-medium text-indigo-600 dark:text-indigo-400"
                : "text-slate-500 hover:text-slate-800 dark:hover:text-slate-200"
            }`}
          >
            {t}
          </button>
        ))}
      </div>

      {isLoading && <Spinner />}
      {error != null && <ErrorBox title="Could not load resource" message={(error as Error).message} />}

      {data && tab === "Overview" && (
        <div className="card p-4">
          <dl className="grid gap-x-8 gap-y-3 text-sm sm:grid-cols-2">
            <Item label="Kind" value={data.kind} />
            <Item label="Name" value={data.name} />
            {data.namespace && <Item label="Namespace" value={data.namespace} />}
            <Item label="Age" value={`${timeAgo(data.createdAt)} (${new Date(data.createdAt).toLocaleString()})`} />
          </dl>
          {data.labels && Object.keys(data.labels).length > 0 && (
            <div className="mt-4">
              <div className="mb-1 text-xs font-semibold uppercase tracking-wide text-slate-400">
                Labels
              </div>
              <div className="flex flex-wrap gap-1.5">
                {Object.entries(data.labels).map(([k, v]) => (
                  <span
                    key={k}
                    className="rounded bg-slate-100 px-2 py-0.5 text-xs text-slate-600 dark:bg-slate-800 dark:text-slate-300"
                  >
                    {k}={v}
                  </span>
                ))}
              </div>
            </div>
          )}
          <p className="mt-4 text-xs text-slate-400">
            Full manifest is on the YAML tab (editable).
          </p>
        </div>
      )}

      {data && tab === "YAML" && (
        <Suspense fallback={<Spinner label="Loading editor..." />}>
          <YamlEditor
            kind={kind}
            namespace={namespace}
            name={name}
            initialYaml={data.yaml}
            dark={dark}
          />
        </Suspense>
      )}

      {tab === "Events" && (
        <div className="card overflow-x-auto">
          {resourceEvents.length === 0 ? (
            <p className="p-4 text-sm text-slate-500">No events for this resource.</p>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-slate-200 text-left text-xs uppercase tracking-wide text-slate-400 dark:border-slate-800">
                  <th className="px-4 py-2.5">Type</th>
                  <th className="px-4 py-2.5">Reason</th>
                  <th className="px-4 py-2.5">Message</th>
                  <th className="px-4 py-2.5">Count</th>
                  <th className="px-4 py-2.5">Last seen</th>
                </tr>
              </thead>
              <tbody>
                {resourceEvents.map((e, i) => (
                  <tr key={i} className="border-b border-slate-100 last:border-0 dark:border-slate-800/50">
                    <td className="px-4 py-2">
                      <StatusBadge status={e.type} />
                    </td>
                    <td className="px-4 py-2 font-medium">{e.reason}</td>
                    <td className="px-4 py-2 text-slate-600 dark:text-slate-300">{e.message}</td>
                    <td className="px-4 py-2 text-slate-500">{e.count}</td>
                    <td className="px-4 py-2 text-slate-500">{timeAgo(e.lastSeen)} ago</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}

      {isPod && tab === "Logs" && <LogsView namespace={namespace} name={name} />}
      {isPod && tab === "Terminal" && <TerminalView namespace={namespace} name={name} />}
    </div>
  );
}

function Item({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-xs font-semibold uppercase tracking-wide text-slate-400">{label}</dt>
      <dd className="mt-0.5">{value}</dd>
    </div>
  );
}
