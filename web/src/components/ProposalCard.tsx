import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type Proposal } from "../api/client";

// ProposalCard renders one remediation proposal with a diff and
// Approve/Reject. Used both in the dashboard Pending-changes panel and inline
// in the AI Assistant chat. Given `initial` it can render immediately, then
// keep the status fresh by id (so a reopened chat shows applied/rejected).
export default function ProposalCard({
  id,
  initial,
  compact = false,
}: {
  id: string;
  initial?: Proposal;
  compact?: boolean;
}) {
  const queryClient = useQueryClient();
  const [showDiff, setShowDiff] = useState(false);
  const [note, setNote] = useState("");

  const { data } = useQuery({
    queryKey: ["proposal", id],
    queryFn: async () => {
      const res = await api.proposals(false);
      return res.proposals.find((p) => p.id === id);
    },
    initialData: initial,
    refetchInterval: (q) => (q.state.data?.status === "pending" ? 5000 : false),
  });
  const proposal = data ?? initial;
  if (!proposal) return null;

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ["proposal", id] });
    queryClient.invalidateQueries({ queryKey: ["proposals"] });
    queryClient.invalidateQueries({ queryKey: ["overview"] });
  };

  const apply = useMutation({
    mutationFn: () => api.applyProposal(proposal.id),
    onSuccess: () => {
      setNote("✓ Applied.");
      invalidate();
    },
    onError: (e) => setNote(`Apply failed: ${(e as Error).message}`),
  });
  const reject = useMutation({
    mutationFn: () => api.rejectProposal(proposal.id),
    onSuccess: invalidate,
    onError: (e) => setNote(`Reject failed: ${(e as Error).message}`),
  });
  const busy = apply.isPending || reject.isPending;
  const pending = proposal.status === "pending";

  const diff = useMemo(
    () => lineDiff(proposal.currentYaml, proposal.proposedYaml),
    [proposal.currentYaml, proposal.proposedYaml],
  );

  return (
    <div className={`card border-amber-300 p-4 dark:border-amber-800 ${compact ? "my-2" : ""}`}>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="text-sm font-medium">
            Proposed change · {proposal.kind}{" "}
            <span className="text-slate-500">{proposal.namespace}/{proposal.name}</span>
          </div>
          {proposal.rationale && (
            <p className="mt-0.5 text-sm text-slate-600 dark:text-slate-300">{proposal.rationale}</p>
          )}
        </div>
        {pending ? (
          <div className="flex shrink-0 gap-2">
            <button
              onClick={() => apply.mutate()}
              disabled={busy}
              className="rounded-lg bg-emerald-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-emerald-500 disabled:opacity-50"
            >
              {apply.isPending ? "Applying…" : "Approve & apply"}
            </button>
            <button
              onClick={() => reject.mutate()}
              disabled={busy}
              className="btn-ghost border border-slate-300 text-sm dark:border-slate-700"
            >
              Reject
            </button>
          </div>
        ) : (
          <span
            className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${
              proposal.status === "applied"
                ? "bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300"
                : proposal.status === "rejected"
                  ? "bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300"
                  : "bg-red-100 text-red-700 dark:bg-red-950 dark:text-red-300"
            }`}
          >
            {proposal.status}
          </span>
        )}
      </div>

      <button
        onClick={() => setShowDiff((v) => !v)}
        className="mt-2 text-xs text-indigo-600 hover:underline dark:text-indigo-400"
      >
        {showDiff ? "Hide diff" : "Review diff"}
      </button>
      {showDiff && (
        <pre className="mt-2 max-h-80 overflow-auto rounded-lg bg-slate-950 p-3 font-mono text-xs leading-5">
          {diff.map((l, i) => (
            <div
              key={i}
              className={
                l.type === "add"
                  ? "bg-emerald-950/60 text-emerald-300"
                  : l.type === "del"
                    ? "bg-red-950/60 text-red-300"
                    : "text-slate-400"
              }
            >
              {l.type === "add" ? "+ " : l.type === "del" ? "- " : "  "}
              {l.text}
            </div>
          ))}
        </pre>
      )}
      {(note || proposal.error) && (
        <p className="mt-2 text-xs text-slate-500 dark:text-slate-400">{note || proposal.error}</p>
      )}
    </div>
  );
}

type DiffLine = { type: "same" | "add" | "del"; text: string };

// lineDiff is a compact LCS line diff — enough to make the change reviewable.
function lineDiff(before: string, after: string): DiffLine[] {
  const a = before.split("\n");
  const b = after.split("\n");
  const m = a.length;
  const n = b.length;
  const lcs: number[][] = Array.from({ length: m + 1 }, () => new Array(n + 1).fill(0));
  for (let i = m - 1; i >= 0; i--) {
    for (let j = n - 1; j >= 0; j--) {
      lcs[i][j] = a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
    }
  }
  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < m && j < n) {
    if (a[i] === b[j]) {
      out.push({ type: "same", text: a[i] });
      i++;
      j++;
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
      out.push({ type: "del", text: a[i] });
      i++;
    } else {
      out.push({ type: "add", text: b[j] });
      j++;
    }
  }
  while (i < m) out.push({ type: "del", text: a[i++] });
  while (j < n) out.push({ type: "add", text: b[j++] });
  return out;
}
