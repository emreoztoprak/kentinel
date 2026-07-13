import type { ReactNode } from "react";
import { statusTone } from "../util";

export function StatusBadge({ status }: { status: string }) {
  if (!status) return null;
  const tone = statusTone(status);
  const tones: Record<string, string> = {
    ok: "bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300",
    warn: "bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300",
    bad: "bg-red-100 text-red-700 dark:bg-red-950 dark:text-red-300",
    neutral: "bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300",
  };
  return (
    <span className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${tones[tone]}`}>
      {status}
    </span>
  );
}

export function ErrorBox({ title, message }: { title?: string; message: string }) {
  return (
    <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200">
      {title && <div className="font-semibold">{title}</div>}
      <div className="break-words">{message}</div>
    </div>
  );
}

export function Spinner({ label }: { label?: string }) {
  return (
    <div className="flex items-center gap-2 py-8 text-sm text-slate-500">
      <span className="h-4 w-4 animate-spin rounded-full border-2 border-slate-300 border-t-indigo-600" />
      {label ?? "Loading..."}
    </div>
  );
}

export function PageTitle({ children, actions }: { children: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mb-4 flex items-center justify-between gap-4">
      <h1 className="text-xl font-semibold">{children}</h1>
      {actions}
    </div>
  );
}

export function EmptyState({ message }: { message: string }) {
  return (
    <div className="py-12 text-center text-sm text-slate-500 dark:text-slate-400">{message}</div>
  );
}
