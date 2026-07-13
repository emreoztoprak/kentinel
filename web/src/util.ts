// timeAgo renders a compact k8s-style age ("3m", "2h", "5d").
export function timeAgo(iso: string): string {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return "-";
  const seconds = Math.max(0, Math.floor((Date.now() - then) / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h`;
  const days = Math.floor(hours / 24);
  return `${days}d`;
}

// statusTone maps a status-ish string to a badge tone.
export function statusTone(status: string): "ok" | "warn" | "bad" | "neutral" {
  const s = status.toLowerCase();
  if (["running", "ready", "healthy", "active", "bound", "succeeded", "normal", "true"].includes(s))
    return "ok";
  if (["pending", "warning", "terminating", "suspended"].includes(s)) return "warn";
  if (
    [
      "failed",
      "critical",
      "error",
      "crashloopbackoff",
      "imagepullbackoff",
      "errimagepull",
      "notready",
      "evicted",
      "oomkilled",
      "createcontainerconfigerror",
    ].includes(s)
  )
    return "bad";
  return "neutral";
}
