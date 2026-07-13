import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";

// LogsView tails pod logs. Follow mode uses the backend's SSE stream via
// EventSource; static mode fetches the last N lines as plain text.
export default function LogsView({ namespace, name }: { namespace: string; name: string }) {
  const { data: containers } = useQuery({
    queryKey: ["containers", namespace, name],
    queryFn: () => api.podContainers(namespace, name),
  });

  const [container, setContainer] = useState("");
  const [follow, setFollow] = useState(true);
  const [tailLines, setTailLines] = useState(500);
  const [previous, setPrevious] = useState(false);
  const [lines, setLines] = useState<string[]>([]);
  const [error, setError] = useState("");
  const bottomRef = useRef<HTMLDivElement>(null);
  const stickToBottom = useRef(true);

  const effectiveContainer = container || containers?.[0] || "";

  useEffect(() => {
    setLines([]);
    setError("");
    if (!effectiveContainer && containers === undefined) return;

    const params = new URLSearchParams({
      container: effectiveContainer,
      tailLines: String(tailLines),
      previous: String(previous),
    });

    if (!follow) {
      const controller = new AbortController();
      fetch(`/api/v1/pods/${namespace}/${name}/logs?${params}`, { signal: controller.signal })
        .then(async (res) => {
          if (!res.ok) {
            const body = await res.json().catch(() => null);
            throw new Error(body?.message ?? `failed to fetch logs (${res.status})`);
          }
          return res.text();
        })
        .then((text) => setLines(text ? text.split("\n") : []))
        .catch((err) => {
          if (!controller.signal.aborted) setError(err.message);
        });
      return () => controller.abort();
    }

    params.set("follow", "true");
    const source = new EventSource(`/api/v1/pods/${namespace}/${name}/logs?${params}`);
    source.addEventListener("log", (ev) => {
      setLines((prev) => {
        const next = [...prev, (ev as MessageEvent).data as string];
        // Keep the DOM bounded during long follows.
        return next.length > 5000 ? next.slice(next.length - 5000) : next;
      });
    });
    source.addEventListener("error", (ev) => {
      const message = (ev as MessageEvent).data;
      if (typeof message === "string" && message) setError(message);
      source.close();
    });
    source.onerror = () => source.close();
    return () => source.close();
  }, [namespace, name, effectiveContainer, follow, tailLines, previous, containers]);

  useEffect(() => {
    if (stickToBottom.current) bottomRef.current?.scrollIntoView({ behavior: "auto" });
  }, [lines]);

  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center gap-3 text-sm">
        <select className="input" value={container} onChange={(e) => setContainer(e.target.value)}>
          {(containers ?? []).map((c) => (
            <option key={c} value={c}>
              {c}
            </option>
          ))}
        </select>
        <select
          className="input"
          value={tailLines}
          onChange={(e) => setTailLines(Number(e.target.value))}
        >
          {[100, 500, 1000, 5000].map((n) => (
            <option key={n} value={n}>
              last {n} lines
            </option>
          ))}
        </select>
        <label className="flex items-center gap-1.5">
          <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} />
          Follow
        </label>
        <label className="flex items-center gap-1.5" title="Logs of the previous (crashed) instance">
          <input
            type="checkbox"
            checked={previous}
            onChange={(e) => setPrevious(e.target.checked)}
          />
          Previous
        </label>
      </div>

      {error && <div className="mb-2 text-sm text-red-500">{error}</div>}

      <div
        className="h-[60vh] overflow-y-auto rounded-lg bg-slate-950 p-3 font-mono text-xs leading-5 text-slate-200"
        onScroll={(e) => {
          const el = e.currentTarget;
          stickToBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
        }}
      >
        {lines.length === 0 && !error && <span className="text-slate-500">No log output.</span>}
        {lines.map((line, i) => (
          <div key={i} className="whitespace-pre-wrap break-all">
            {line}
          </div>
        ))}
        <div ref={bottomRef} />
      </div>
    </div>
  );
}
