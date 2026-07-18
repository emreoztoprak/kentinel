import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import ProposalCard from "./ProposalCard";

// ProposalsPanel shows the agent's pending remediation proposals on the
// dashboard. Approval also happens inline in the AI Assistant chat; this
// panel is the cross-conversation view of everything still awaiting a
// decision. Rendered only in assisted mode.
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
          <ProposalCard key={p.id} id={p.id} initial={p} />
        ))}
      </div>
    </div>
  );
}
