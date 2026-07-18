import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";

// UsageCard shows Kentinel's own LLM token usage over the last 30 days, plus
// an estimated cost for priced (cloud) providers. It is a rough estimate —
// hardcoded prices, no caching/batch discounts — and reflects only Kentinel's
// calls, not your whole account. Ollama shows tokens only (local = free).
export default function UsageCard() {
  const { data } = useQuery({
    queryKey: ["usage", 30],
    queryFn: () => api.usage(30),
    refetchInterval: 60_000,
  });
  if (!data || (data.inputTokens === 0 && data.outputTokens === 0)) return null;

  const total = fmtTokens(data.inputTokens + data.outputTokens);
  const review = data.bySource.find((s) => s.source === "review");
  const query = data.bySource.find((s) => s.source === "query");

  return (
    <div className="card p-4">
      <div className="mb-2 flex items-baseline justify-between">
        <h2 className="font-semibold">LLM usage · last 30 days</h2>
        {data.hasPricing ? (
          <span className="text-lg font-semibold">≈ ${data.costUsd.toFixed(2)}</span>
        ) : (
          <span className="text-xs text-slate-400">local model — no API cost</span>
        )}
      </div>

      <div className="grid grid-cols-3 gap-3 text-sm">
        <Stat label="Total tokens" value={total} />
        <Stat label="Input" value={fmtTokens(data.inputTokens)} />
        <Stat label="Output" value={fmtTokens(data.outputTokens)} />
      </div>

      <div className="mt-3 flex flex-wrap gap-x-6 gap-y-1 text-xs text-slate-500 dark:text-slate-400">
        {review && (
          <span>
            Periodic review: {review.calls} calls · {fmtTokens(review.inputTokens + review.outputTokens)} tok
            {data.hasPricing ? ` · ≈ $${review.costUsd.toFixed(2)}` : ""}
          </span>
        )}
        {query && (
          <span>
            Assistant: {query.calls} calls · {fmtTokens(query.inputTokens + query.outputTokens)} tok
            {data.hasPricing ? ` · ≈ $${query.costUsd.toFixed(2)}` : ""}
          </span>
        )}
      </div>

      {data.hasPricing && (
        <p className="mt-2 text-xs text-slate-400">
          Estimate only ({data.model}) — based on published prices, excludes caching/batch
          discounts, and counts only Kentinel's calls (not your whole account).
        </p>
      )}
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-lg font-semibold">{value}</div>
      <div className="text-xs text-slate-400">{label}</div>
    </div>
  );
}

function fmtTokens(n: number): string {
  if (n >= 1e6) return (n / 1e6).toFixed(1) + "M";
  if (n >= 1e3) return (n / 1e3).toFixed(1) + "K";
  return String(n);
}
