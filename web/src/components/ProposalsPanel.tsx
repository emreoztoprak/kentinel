import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type Proposal } from "../api/client";

// ProposalsPanel shows the agent's pending remediation proposals with a diff
// and Approve/Reject. Rendered only in assisted mode. Approving sends the
// change to the SERVER, which applies it (the agent never applies).
export default function ProposalsPanel() {
  const { data } = useQuery({
    queryKey: ["proposals", "pending"],
    queryFn: () => api.proposals(true),
    refetchInterval: 8000,
  });
  const pending = data?.proposals ?? [];
  if (pending.length === 0) return null;

  return (
    <div className="mb-6">
      <div className="mb-2 flex items-center gap-2">
        <h2 className="font-semibold">Pending changes</h2>
        <span className="rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-950 dark:text-amber-300">
          {pending.length} awaiting approval
        </span>
      </div>
      <div className="space-y-3">
        {pending.map((p) => (
          <ProposalCard key={p.id} proposal={p} />
        ))}
      </div>
    </div>
  );
}

function ProposalCard({ proposal }: { proposal: Proposal }) {
  const queryClient = useQueryClient();
  const [showDiff, setShowDiff] = useState(false);
  const [note, setNote] = useState("");

  const invalidate = () => {
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

  const diff = useMemo(
    () => lineDiff(proposal.currentYaml, proposal.proposedYaml),
    [proposal.currentYaml, proposal.proposedYaml],
  );

  return (
    <div className="card border-amber-300 p-4 dark:border-amber-800">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="text-sm font-medium">
            {proposal.kind} <span className="text-slate-500">{proposal.namespace}/{proposal.name}</span>
          </div>
          <p className="mt-0.5 text-sm text-slate-600 dark:text-slate-300">{proposal.rationale}</p>
        </div>
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
      {note && <p className="mt-2 text-xs text-slate-500 dark:text-slate-400">{note}</p>}
    </div>
  );
}

type DiffLine = { type: "same" | "add" | "del"; text: string };

// lineDiff is a compact LCS line diff — enough to make the proposed change
// reviewable. Not a full patch tool; the human is approving the whole
// proposed manifest, the diff just highlights what moved.
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
