import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";

const RELEASES_API = "https://api.github.com/repos/emreoztoprak/kentinel/releases/latest";
const REFETCH_INTERVAL = 6 * 60 * 60 * 1000; // 6h — GitHub's rate limit has no trouble with this

interface GithubRelease {
  tag_name: string;
  html_url: string;
}

// parseSemver extracts [major, minor, patch] from strings like "v0.3.0" or
// "0.3.0". Returns null for anything that doesn't look like a release
// version (e.g. "dev").
function parseSemver(v: string): [number, number, number] | null {
  const m = v.replace(/^v/, "").match(/^(\d+)\.(\d+)\.(\d+)/);
  if (!m) return null;
  return [Number(m[1]), Number(m[2]), Number(m[3])];
}

function isNewer(latest: [number, number, number], current: [number, number, number]): boolean {
  for (let i = 0; i < 3; i++) {
    if (latest[i] > current[i]) return true;
    if (latest[i] < current[i]) return false;
  }
  return false;
}

// UpdateStatus checks GitHub's public releases API directly from the
// browser — not through the server — so a locked-down cluster with no
// outbound internet access from its pods doesn't gain a new dependency;
// only the machine running the browser needs to reach github.com.
export default function UpdateStatus() {
  const serverQuery = useQuery({ queryKey: ["server-settings"], queryFn: api.serverSettings });
  const releaseQuery = useQuery<GithubRelease>({
    queryKey: ["latest-github-release"],
    queryFn: async () => {
      const res = await fetch(RELEASES_API, { headers: { Accept: "application/vnd.github+json" } });
      if (!res.ok) throw new Error(`GitHub API returned ${res.status}`);
      return res.json();
    },
    staleTime: REFETCH_INTERVAL,
    refetchInterval: REFETCH_INTERVAL,
    retry: 1,
  });

  const current = serverQuery.data?.version;
  // "dev" (local/unreleased builds) has nothing meaningful to compare against.
  if (!current || current === "dev") return null;
  // A network hiccup or GitHub rate limit isn't worth alarming anyone over —
  // this is a convenience feature, not a health signal.
  if (releaseQuery.isLoading || releaseQuery.isError || !releaseQuery.data) return null;

  // The GitHub API response is not fully trusted: everything user-visible
  // is rebuilt from the *parsed* semver components, never the raw tag
  // string, so a hostile tag name can't smuggle shell text into the
  // copy-paste command below. Same for the release URL: only a plain
  // github.com release link is accepted.
  const latestParsed = parseSemver(releaseQuery.data.tag_name);
  const currentParsed = parseSemver(current);
  if (!latestParsed || !currentParsed) return null;
  const releaseUrl = releaseQuery.data.html_url;
  const safeReleaseUrl = /^https:\/\/github\.com\//.test(releaseUrl) ? releaseUrl : "";

  if (!isNewer(latestParsed, currentParsed)) {
    return (
      <p className="mb-6 text-xs text-slate-400">
        Kentinel v{current} — you're up to date.
      </p>
    );
  }

  return (
    <div className="mb-6">
      <UpdateAvailableCard
        current={current}
        latestVersion={latestParsed.join(".")}
        releaseUrl={safeReleaseUrl}
        namespace={serverQuery.data?.namespace || "kentinel"}
      />
    </div>
  );
}

function UpdateAvailableCard({
  current,
  latestVersion,
  releaseUrl,
  namespace,
}: {
  current: string;
  latestVersion: string; // rebuilt from parsed semver — never the raw tag
  releaseUrl: string; // "" when the API's html_url failed validation
  namespace: string;
}) {
  const command = `helm upgrade kentinel oci://ghcr.io/emreoztoprak/charts/kentinel \\
  --version ${latestVersion} \\
  -n ${namespace} \\
  --reuse-values \\
  --set image.tag=${latestVersion}`;
  const [copied, setCopied] = useState(false);

  return (
    <div className="card border-indigo-200 p-4 dark:border-indigo-800">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h2 className="font-semibold">Update available</h2>
          <p className="text-sm text-slate-500 dark:text-slate-400">
            v{current} → v{latestVersion}
          </p>
        </div>
        {releaseUrl && (
          <a
            href={releaseUrl}
            target="_blank"
            rel="noreferrer"
            className="text-sm text-indigo-600 hover:underline dark:text-indigo-400"
          >
            Release notes →
          </a>
        )}
      </div>

      <p className="mt-3 text-xs text-slate-500 dark:text-slate-400">
        Kentinel doesn't upgrade itself — run this from wherever you manage the
        cluster:
      </p>
      <div className="relative mt-2">
        <pre className="overflow-x-auto rounded-lg bg-slate-950 p-3 font-mono text-xs leading-5 text-slate-200">
          {command}
        </pre>
        <button
          onClick={() => {
            navigator.clipboard.writeText(command);
            setCopied(true);
            setTimeout(() => setCopied(false), 2000);
          }}
          className="absolute right-2 top-2 rounded bg-slate-800 px-2 py-1 text-xs text-slate-200 hover:bg-slate-700"
        >
          {copied ? "Copied!" : "Copy"}
        </button>
      </div>
      <p className="mt-2 text-xs text-slate-400">
        Using the raw manifests or Docker instead? See{" "}
        <Link to="/docs/deployment" className="text-indigo-600 hover:underline dark:text-indigo-400">
          Deployment docs
        </Link>{" "}
        for the equivalent steps.
      </p>
    </div>
  );
}
