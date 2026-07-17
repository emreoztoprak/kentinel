import { NavLink, useLocation, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { api } from "../api/client";
import { useNamespace, useTheme } from "../context";

const NAV_SECTIONS: { title: string; items: { label: string; to: string }[] }[] = [
  {
    title: "",
    items: [
      { label: "Dashboard", to: "/" },
      { label: "AI Assistant", to: "/assistant" },
      { label: "AI History", to: "/insights" },
      { label: "Events", to: "/events" },
    ],
  },
  {
    title: "Workloads",
    items: [
      { label: "Pods", to: "/resources/pods" },
      { label: "Deployments", to: "/resources/deployments" },
      { label: "StatefulSets", to: "/resources/statefulsets" },
      { label: "DaemonSets", to: "/resources/daemonsets" },
      { label: "Jobs", to: "/resources/jobs" },
      { label: "CronJobs", to: "/resources/cronjobs" },
    ],
  },
  {
    title: "Network",
    items: [
      { label: "Services", to: "/resources/services" },
      { label: "Ingresses", to: "/resources/ingresses" },
    ],
  },
  {
    title: "Config & Storage",
    items: [
      { label: "ConfigMaps", to: "/resources/configmaps" },
      { label: "Secrets", to: "/resources/secrets" },
      { label: "PVCs", to: "/resources/persistentvolumeclaims" },
    ],
  },
  {
    title: "Cluster",
    items: [{ label: "Nodes", to: "/resources/nodes" }],
  },
  {
    title: "System",
    items: [
      { label: "Settings", to: "/settings" },
      { label: "Documentation", to: "/docs" },
    ],
  },
];

// Pages whose data isn't namespace-filtered — the selector would suggest a
// control that silently does nothing.
const NON_NAMESPACED_PATHS = new Set(["/", "/assistant", "/insights", "/settings", "/docs"]);

function isNamespaceScoped(pathname: string): boolean {
  if (NON_NAMESPACED_PATHS.has(pathname) || pathname.startsWith("/docs/")) return false;
  if (pathname === "/resources/nodes") return false; // nodes are cluster-scoped
  return true;
}

export default function Layout({ children }: { children: ReactNode }) {
  const { namespace, setNamespace } = useNamespace();
  const { dark, toggle } = useTheme();
  const navigate = useNavigate();
  const location = useLocation();
  const showNamespace = isNamespaceScoped(location.pathname);

  const { data: namespaces } = useQuery({
    queryKey: ["namespaces"],
    queryFn: api.namespaces,
    refetchInterval: 30_000,
  });

  return (
    <div className="flex h-screen overflow-hidden">
      <aside className="flex w-56 shrink-0 flex-col border-r border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-900">
        <button
          onClick={() => navigate("/")}
          className="flex items-center gap-2 px-4 py-4 text-left"
        >
          <img src="/logo.png" alt="Kentinel" className="h-8 w-8 rounded-lg" />
          <span className="text-sm font-semibold leading-tight">
            Kentinel
          </span>
        </button>

        <nav className="flex-1 space-y-4 overflow-y-auto px-2 pb-4">
          {NAV_SECTIONS.map((section) => (
            <div key={section.title}>
              {section.title && (
                <div className="px-2 pb-1 pt-2 text-xs font-semibold uppercase tracking-wide text-slate-400">
                  {section.title}
                </div>
              )}
              {section.items.map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  end={item.to === "/"}
                  className={({ isActive }) =>
                    `block rounded-lg px-3 py-1.5 text-sm ${
                      isActive
                        ? "bg-indigo-50 font-medium text-indigo-700 dark:bg-indigo-950 dark:text-indigo-300"
                        : "text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800"
                    }`
                  }
                >
                  {item.label}
                </NavLink>
              ))}
            </div>
          ))}
        </nav>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex items-center justify-between gap-4 border-b border-slate-200 bg-white px-6 py-3 dark:border-slate-800 dark:bg-slate-900">
          {showNamespace ? (
            <div className="flex items-center gap-2">
              <label className="text-sm text-slate-500 dark:text-slate-400">Namespace</label>
              <select
                className="input"
                value={namespace}
                onChange={(e) => setNamespace(e.target.value)}
              >
                <option value="">All namespaces</option>
                {(namespaces ?? []).map((ns) => (
                  <option key={ns} value={ns}>
                    {ns}
                  </option>
                ))}
              </select>
            </div>
          ) : (
            <div />
          )}
          <div className="flex items-center gap-3">
            <ModeBadge />
            <button className="btn-ghost" onClick={toggle} title="Toggle theme">
              {dark ? "🌙 Dark" : "☀️ Light"}
            </button>
          </div>
        </header>

        <main className="flex-1 overflow-y-auto p-6">{children}</main>
      </div>
    </div>
  );
}

// ModeBadge shows the deploy-time operating mode so it's always clear whether
// Kentinel can change anything.
function ModeBadge() {
  const { data } = useQuery({ queryKey: ["server-settings"], queryFn: api.serverSettings });
  if (!data) return null;
  const assisted = data.mode === "assisted";
  return (
    <span
      title={
        assisted
          ? "Assisted mode: the agent can propose changes for you to approve."
          : "Read-only mode: Kentinel observes and advises but cannot change anything."
      }
      className={`rounded-full px-2 py-0.5 text-xs font-medium ${
        assisted
          ? "bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300"
          : "bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300"
      }`}
    >
      {assisted ? "Assisted" : "Read-only"}
    </span>
  );
}
