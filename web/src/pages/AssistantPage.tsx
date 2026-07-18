import { useEffect, useRef, useState } from "react";
import { useLocation } from "react-router-dom";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { agentQuery, type Proposal, type QueryEvent, type QueryTurn } from "../api/client";
import { PageTitle } from "../components/ui";
import ProposalCard from "../components/ProposalCard";
import { timeAgo } from "../util";
import {
  type ChatEntry,
  type Conversation,
  deleteConversation,
  loadConversations,
  newConversationId,
  titleFor,
  upsertConversation,
} from "../conversations";

// describeTool turns a raw tool call ("get_pod_logs {\"namespace\":\"app\",
// \"pod\":\"web-1\"}") into a readable activity line ("Reading logs for
// app/web-1"). Users shouldn't see internal function names; this keeps the
// "what's the agent doing" signal without the plumbing.
function describeTool(raw: string): string {
  const space = raw.indexOf(" ");
  const name = space === -1 ? raw : raw.slice(0, space);
  let args: Record<string, unknown> = {};
  if (space !== -1) {
    try {
      args = JSON.parse(raw.slice(space + 1));
    } catch {
      // Non-JSON args — just use the verb below with no target.
    }
  }
  const str = (v: unknown) => (typeof v === "string" && v ? v : "");
  const ns = str(args.namespace);
  const target = [ns, str(args.pod) || str(args.name)].filter(Boolean).join("/");
  const inNs = ns ? ` in ${ns}` : "";
  const forTarget = target ? ` for ${target}` : inNs;

  switch (name) {
    case "get_cluster_overview":
      return "Checking the cluster overview";
    case "list_resources":
      return `Listing ${str(args.kind) || "resources"}${inNs}`;
    case "get_resource":
      return `Inspecting ${str(args.kind) || "a resource"}${target ? ` ${target}` : inNs}`;
    case "get_pod_logs":
      return `Reading logs${forTarget}`;
    case "get_events":
      return `Checking events${inNs}`;
    case "get_resource_usage":
      return `Checking resource usage${inNs}`;
    case "query_metrics":
      return "Querying metrics";
    default:
      return "Inspecting the cluster";
  }
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
  const [convId, setConvId] = useState<string>(() => newConversationId());
  const [conversations, setConversations] = useState<Conversation[]>(() => loadConversations());
  const [showHistory, setShowHistory] = useState(false);
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

  // Persist the current conversation whenever it changes (but not while a
  // response is still streaming, to avoid churn and half-written turns).
  useEffect(() => {
    if (chat.length === 0 || busy) return;
    setConversations(
      upsertConversation({ id: convId, title: titleFor(chat), updatedAt: Date.now(), chat }),
    );
  }, [chat, busy, convId]);

  const startNewChat = () => {
    abortRef.current?.();
    setBusy(false);
    setChat([]);
    setConvId(newConversationId());
    setShowHistory(false);
  };

  const openConversation = (c: Conversation) => {
    abortRef.current?.();
    setBusy(false);
    setConvId(c.id);
    setChat(c.chat);
    setShowHistory(false);
  };

  const removeConversation = (id: string) => {
    setConversations(deleteConversation(id));
    if (id === convId) startNewChat();
  };

  const send = (prompt: string) => {
    const trimmed = prompt.trim();
    if (!trimmed || busy) return;
    setInput("");
    setBusy(true);

    // Build the conversation to send: prior turns (assistant text only, tool
    // steps dropped) plus this new user message — so the agent has context.
    const history: QueryTurn[] = chat
      .map((e) => ({
        role: e.role,
        text: e.steps
          .filter((s) => s.kind === "text")
          .map((s) => s.content)
          .join("")
          .trim(),
      }))
      .filter((m) => m.text);
    history.push({ role: "user", text: trimmed });

    setChat((prev) => [
      ...prev,
      { role: "user", steps: [{ kind: "text", content: trimmed }], done: true },
      { role: "assistant", steps: [], done: false },
    ]);

    const appendStep = (step: ChatEntry["steps"][number], done = false) =>
      setChat((prev) => {
        const next = [...prev];
        const last = { ...next[next.length - 1] };
        const steps = [...last.steps];
        if (step.content) {
          const tail = steps[steps.length - 1];
          // Coalesce consecutive streamed text deltas into one block so
          // multi-line markdown (tables, lists) renders as a whole rather
          // than as per-token fragments that never form valid markdown.
          if (step.kind === "text" && tail?.kind === "text") {
            steps[steps.length - 1] = { ...tail, content: tail.content + step.content };
          } else {
            steps.push(step);
          }
        }
        last.steps = steps;
        last.done = done || last.done;
        next[next.length - 1] = last;
        return next;
      });

    abortRef.current = agentQuery(
      history,
      (ev: QueryEvent) => {
        if (ev.type === "text") appendStep({ kind: "text", content: ev.content });
        else if (ev.type === "tool") appendStep({ kind: "tool", content: ev.content });
        else if (ev.type === "proposal") appendStep({ kind: "proposal", content: ev.content });
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
      <PageTitle
        actions={
          <div className="relative flex items-center gap-2">
            <button
              className="btn-ghost border border-slate-300 text-sm dark:border-slate-700"
              onClick={() => setShowHistory((v) => !v)}
            >
              History{conversations.length > 0 ? ` (${conversations.length})` : ""}
            </button>
            <button
              className="btn-ghost border border-slate-300 text-sm dark:border-slate-700"
              onClick={startNewChat}
            >
              + New chat
            </button>
            {showHistory && (
              <div className="absolute right-0 top-full z-10 mt-2 max-h-96 w-80 overflow-y-auto rounded-lg border border-slate-200 bg-white p-1 shadow-lg dark:border-slate-700 dark:bg-slate-900">
                {conversations.length === 0 ? (
                  <p className="px-3 py-4 text-center text-sm text-slate-400">No past conversations yet.</p>
                ) : (
                  conversations.map((c) => (
                    <div
                      key={c.id}
                      className={`group flex items-center gap-2 rounded-md px-2 py-2 text-sm hover:bg-slate-100 dark:hover:bg-slate-800 ${
                        c.id === convId ? "bg-slate-100 dark:bg-slate-800" : ""
                      }`}
                    >
                      <button className="min-w-0 flex-1 text-left" onClick={() => openConversation(c)}>
                        <div className="truncate">{c.title}</div>
                        <div className="text-xs text-slate-400">{timeAgo(new Date(c.updatedAt).toISOString())} ago</div>
                      </button>
                      <button
                        className="shrink-0 text-xs text-slate-400 opacity-0 hover:text-red-500 group-hover:opacity-100"
                        title="Delete conversation"
                        onClick={() => removeConversation(c.id)}
                      >
                        ✕
                      </button>
                    </div>
                  ))
                )}
              </div>
            )}
          </div>
        }
      >
        AI Assistant
      </PageTitle>

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
                      className="my-1.5 flex items-center gap-1.5 truncate px-1 py-0.5 text-xs italic text-slate-400 dark:text-slate-500"
                    >
                      <span className="not-italic">🔍</span> {describeTool(step.content)}
                    </div>
                  ) : step.kind === "proposal" ? (
                    <InlineProposal key={j} json={step.content} />
                  ) : step.kind === "error" ? (
                    <div key={j} className="my-1.5 text-sm text-red-500">
                      {step.content}
                    </div>
                  ) : step.content ? (
                    <div
                      key={j}
                      className="max-w-none py-1 text-sm [&_code]:rounded [&_code]:bg-slate-100 [&_code]:px-1 [&_code]:text-xs dark:[&_code]:bg-slate-800 [&_h1]:my-2 [&_h1]:text-base [&_h1]:font-semibold [&_h2]:my-2 [&_h2]:text-sm [&_h2]:font-semibold [&_h3]:my-2 [&_h3]:text-sm [&_h3]:font-semibold [&_li]:my-0.5 [&_ol]:my-1.5 [&_ol]:list-decimal [&_ol]:pl-5 [&_p]:my-1.5 [&_pre]:overflow-x-auto [&_pre]:rounded [&_pre]:bg-slate-950 [&_pre]:p-2 [&_pre]:text-xs [&_pre]:text-slate-100 [&_pre_code]:bg-transparent [&_pre_code]:text-slate-100 [&_ul]:my-1.5 [&_ul]:list-disc [&_ul]:pl-5"
                    >
                      <ReactMarkdown
                        remarkPlugins={[remarkGfm]}
                        components={{
                          table: ({ children }) => (
                            <div className="my-2 overflow-x-auto">
                              <table className="w-full border-collapse text-xs">{children}</table>
                            </div>
                          ),
                          th: ({ children }) => (
                            <th className="border-b border-slate-300 px-2 py-1 text-left font-semibold dark:border-slate-600">
                              {children}
                            </th>
                          ),
                          td: ({ children }) => (
                            <td className="border-b border-slate-100 px-2 py-1 align-top dark:border-slate-800">
                              {children}
                            </td>
                          ),
                        }}
                      >
                        {step.content}
                      </ReactMarkdown>
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

// InlineProposal renders a remediation proposal (streamed as JSON) as an
// approval card right inside the chat, so the user approves without leaving
// the conversation.
function InlineProposal({ json }: { json: string }) {
  let proposal: Proposal | null = null;
  try {
    proposal = JSON.parse(json) as Proposal;
  } catch {
    proposal = null;
  }
  if (!proposal) return null;
  return (
    <div className="my-2">
      <ProposalCard id={proposal.id} initial={proposal} compact />
    </div>
  );
}
