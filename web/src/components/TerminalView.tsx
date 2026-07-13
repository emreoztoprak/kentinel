import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import "@xterm/xterm/css/xterm.css";

// TerminalView opens an interactive shell in the pod via the backend's exec
// WebSocket. Message protocol matches internal/server/exec.go.
export default function TerminalView({ namespace, name }: { namespace: string; name: string }) {
  const { data: containers } = useQuery({
    queryKey: ["containers", namespace, name],
    queryFn: () => api.podContainers(namespace, name),
  });

  const [container, setContainer] = useState("");
  const [shell, setShell] = useState("/bin/sh");
  const [connected, setConnected] = useState(false);
  const [sessionKey, setSessionKey] = useState(0); // bump to reconnect
  const hostRef = useRef<HTMLDivElement>(null);

  const effectiveContainer = container || containers?.[0] || "";

  useEffect(() => {
    if (!hostRef.current || !effectiveContainer) return;

    const term = new Terminal({
      fontSize: 13,
      cursorBlink: true,
      theme: { background: "#020617" },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(hostRef.current);
    fit.fit();

    const proto = window.location.protocol === "https:" ? "wss" : "ws";
    const params = new URLSearchParams({ container: effectiveContainer, command: shell });
    const ws = new WebSocket(
      `${proto}://${window.location.host}/api/v1/pods/${namespace}/${name}/exec?${params}`,
    );

    const sendResize = () =>
      ws.readyState === WebSocket.OPEN &&
      ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));

    ws.onopen = () => {
      setConnected(true);
      sendResize();
      term.focus();
    };
    ws.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data);
        if (msg.type === "stdout") term.write(msg.data);
        else if (msg.type === "error") term.writeln(`\r\n\x1b[31m${msg.data}\x1b[0m`);
        else if (msg.type === "exit") term.writeln("\r\n\x1b[33m[session ended]\x1b[0m");
      } catch {
        /* ignore malformed frames */
      }
    };
    ws.onclose = () => setConnected(false);

    const dataSub = term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "stdin", data }));
    });
    const resizeSub = term.onResize(() => sendResize());
    const onWindowResize = () => fit.fit();
    window.addEventListener("resize", onWindowResize);

    return () => {
      window.removeEventListener("resize", onWindowResize);
      dataSub.dispose();
      resizeSub.dispose();
      ws.close();
      term.dispose();
    };
  }, [namespace, name, effectiveContainer, shell, sessionKey]);

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
        <select className="input" value={shell} onChange={(e) => setShell(e.target.value)}>
          <option value="/bin/sh">/bin/sh</option>
          <option value="/bin/bash">/bin/bash</option>
        </select>
        <button className="btn-ghost" onClick={() => setSessionKey((k) => k + 1)}>
          Reconnect
        </button>
        <span className={`text-xs ${connected ? "text-emerald-500" : "text-slate-400"}`}>
          {connected ? "● connected" : "○ disconnected"}
        </span>
      </div>
      <div ref={hostRef} className="h-[60vh] overflow-hidden rounded-lg bg-[#020617] p-2" />
    </div>
  );
}
