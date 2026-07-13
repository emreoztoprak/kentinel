// The documentation shown on the /docs page. Markdown files are bundled into
// the SPA at build time (?raw imports), so the docs page works identically in
// dev, Docker, and in-cluster mode and always matches the deployed version.
import readme from "../../README.md?raw";
import architecture from "../../docs/architecture.md?raw";
import deployment from "../../docs/deployment.md?raw";
import configuration from "../../docs/configuration.md?raw";
import aiAgent from "../../docs/ai-agent.md?raw";
import apiRef from "../../docs/api.md?raw";
import security from "../../docs/security.md?raw";

export interface Doc {
  slug: string;
  title: string;
  description: string;
  content: string;
}

// The README opens with a raw-HTML logo block for GitHub. react-markdown
// (correctly) doesn't render raw HTML, so strip HTML blocks for the in-app
// view — the app shows the logo in the sidebar already.
function stripHTMLBlocks(markdown: string): string {
  return markdown.replace(/<p[^>]*>[\s\S]*?<\/p>\s*/gi, "");
}

export const DOCS: Doc[] = [
  { slug: "overview", title: "Overview", description: "What this is and quickstarts", content: stripHTMLBlocks(readme) },
  { slug: "architecture", title: "Architecture", description: "Components, data flow, RBAC", content: architecture },
  { slug: "deployment", title: "Deployment", description: "Docker mode, in-cluster mode", content: deployment },
  { slug: "configuration", title: "Configuration", description: "Env vars, LLM providers, Settings UI", content: configuration },
  { slug: "ai-agent", title: "AI Agent", description: "Review loop, query tools, cost", content: aiAgent },
  { slug: "api", title: "API Reference", description: "REST / SSE / WebSocket endpoints", content: apiRef },
  { slug: "security", title: "Security", description: "Threat model and hardening", content: security },
];

// Maps markdown file references (docs/deployment.md, ./api.md, ../README.md)
// to in-app doc routes. Returns null for links that should stay as-is.
export function docRouteForHref(href: string): string | null {
  if (/^[a-z]+:\/\//i.test(href) || href.startsWith("mailto:")) return null;

  const [path, hash] = href.split("#");
  if (!path) return hash ? `#${hash}` : null; // same-page anchor

  const file = path.replace(/^(\.\/|\.\.\/|docs\/)+/, "");
  const match = file.match(/^([A-Za-z0-9-]+)\.md$/);
  if (!match) return null;

  const slug = match[1].toLowerCase() === "readme" ? "overview" : match[1];
  if (!DOCS.some((d) => d.slug === slug)) return null;
  return `/docs/${slug}${hash ? `#${hash}` : ""}`;
}
