import { useEffect, useRef, useState } from "react";
import { useLocation } from "react-router-dom";
import ReactMarkdown from "react-markdown";
import { agentQuery, type QueryEvent } from "../api/client";
import { PageTitle } from "../components/ui";

interface ChatEntry {
  role: "user" | "assistant";
  // Assistant entries are a sequence of steps: text blocks and tool calls.
  steps: { kind: "text" | "tool" | "error"; content: string }[];
  done: boolean;
}

const SUGGESTIONS = [
  "What's wrong with my cluster right now?",
  "Which pods restarted recently and why?",
  "Summarize the warning events from the last hour.",
  "Are any deployments not fully available?",
];

export default function AssistantPage() {
  const location = useLocation();
  const [input, setInput] = useState("");
  const [chat, setChat] = useState<ChatEntry[]>([]);
  const [busy, setBusy] = useState(false);
  const abortRef = useRef<(() => void) | null>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const prefilled = useRef(false);

  // "Ask AI about this resource" navigates here with a prefilled prompt.
  useEffect(() => {
    const prompt = (location.state as { prompt?: string } | null)?.prompt;
    if (prompt && !prefilled.current) {
      prefilled.current = true;
      setInput(prompt);
    }
  }, [location.state]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [chat]);

  useEffect(() => () => abortRef.current?.(), []);

  const send = (prompt: string) => {
    const trimmed = prompt.trim();
    if (!trimmed || busy) return;
    setInput("");
    setBusy(true);
    setChat((prev) => [
      ...prev,
      { role: "user", steps: [{ kind: "text", content: trimmed }], done: true },
      { role: "assistant", steps: [], done: false },
    ]);

    const appendStep = (step: ChatEntry["steps"][number], done = false) =>
      setChat((prev) => {
        const next = [...prev];
        const last = { ...next[next.length - 1] };
        if (step.content) last.steps = [...last.steps, step];
        last.done = done || last.done;
        next[next.length - 1] = last;
        return next;
      });

    abortRef.current = agentQuery(
      trimmed,
      (ev: QueryEvent) => {
        if (ev.type === "text") appendStep({ kind: "text", content: ev.content });
        else if (ev.type === "tool") appendStep({ kind: "tool", content: ev.content });
        else if (ev.type === "error") {
          appendStep({ kind: "error", content: ev.content }, true);
          setBusy(false);
        } else if (ev.type === "done") {
          appendStep({ kind: "text", content: "" }, true);
          setBusy(false);
        }
      },
      (message) => {
        appendStep({ kind: "error", content: message }, true);
        setBusy(false);
      },
    );
  };

  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col">
      <PageTitle>AI Assistant</PageTitle>

      <div className="flex-1 space-y-4 overflow-y-auto pb-4">
        {chat.length === 0 && (
          <div className="card p-6 text-sm text-slate-500">
            <p className="mb-3">
              Ask anything about this cluster. The assistant inspects resources, logs and events
              with read-only access — it never modifies anything.
            </p>
            <div className="flex flex-wrap gap-2">
              {SUGGESTIONS.map((s) => (
                <button
                  key={s}
                  className="rounded-full border border-slate-300 px-3 py-1 text-xs hover:border-indigo-400 hover:text-indigo-500 dark:border-slate-700"
                  onClick={() => send(s)}
                >
                  {s}
                </button>
              ))}
            </div>
          </div>
        )}

        {chat.map((entry, i) => (
          <div key={i} className={entry.role === "user" ? "flex justify-end" : ""}>
            {entry.role === "user" ? (
              <div className="max-w-[85%] rounded-2xl rounded-br-sm bg-indigo-600 px-4 py-2 text-sm text-white">
                {entry.steps[0]?.content}
              </div>
            ) : (
              <div className="card max-w-[95%] px-4 py-3">
                {entry.steps.length === 0 && !entry.done && (
                  <span className="text-sm text-slate-400">Thinking…</span>
                )}
                {entry.steps.map((step, j) =>
                  step.kind === "tool" ? (
                    <div
                      key={j}
                      className="my-1.5 truncate rounded bg-slate-100 px-2 py-1 font-mono text-xs text-slate-500 dark:bg-slate-800 dark:text-slate-400"
                      title={step.content}
                    >
                      🔧 {step.content}
                    </div>
                  ) : step.kind === "error" ? (
                    <div key={j} className="my-1.5 text-sm text-red-500">
                      {step.content}
                    </div>
                  ) : step.content ? (
                    <div
                      key={j}
                      className="prose prose-sm max-w-none py-1 text-sm dark:prose-invert [&_code]:rounded [&_code]:bg-slate-100 [&_code]:px-1 [&_code]:text-xs dark:[&_code]:bg-slate-800 [&_h1]:text-base [&_h2]:text-sm [&_h3]:text-sm [&_li]:my-0.5 [&_ol]:list-decimal [&_ol]:pl-5 [&_p]:my-1.5 [&_ul]:list-disc [&_ul]:pl-5"
                    >
                      <ReactMarkdown>{step.content}</ReactMarkdown>
                    </div>
                  ) : null,
                )}
                {!entry.done && entry.steps.length > 0 && (
                  <span className="text-xs text-slate-400">working…</span>
                )}
              </div>
            )}
          </div>
        ))}
        <div ref={bottomRef} />
      </div>

      <form
        className="flex gap-2 border-t border-slate-200 pt-4 dark:border-slate-800"
        onSubmit={(e) => {
          e.preventDefault();
          send(input);
        }}
      >
        <textarea
          className="input flex-1 resize-none"
          rows={2}
          placeholder='e.g. "analyze logs on demo-app in app namespace for last 10 minutes"'
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              send(input);
            }
          }}
        />
        <button className="btn-primary self-end" type="submit" disabled={busy || !input.trim()}>
          {busy ? "Working…" : "Send"}
        </button>
      </form>
    </div>
  );
}
