import { useEffect } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { DOCS, docRouteForHref } from "../docs";

// The same docs, hosted on GitHub Pages — handy for sharing a URL. The
// bundled copy stays primary: it matches the deployed version and works
// offline / air-gapped.
const DOCS_SITE_URL = "https://emreoztoprak.github.io/kentinel/";

export default function DocsPage() {
  const { slug = "overview" } = useParams();
  const navigate = useNavigate();
  const doc = DOCS.find((d) => d.slug === slug) ?? DOCS[0];

  // Scroll to top (or to the anchor) when switching docs.
  useEffect(() => {
    const hash = window.location.hash.slice(1);
    if (hash) {
      document.getElementById(hash)?.scrollIntoView();
    } else {
      document.querySelector("main")?.scrollTo(0, 0);
    }
  }, [slug]);

  return (
    <div className="flex gap-8">
      <aside className="w-56 shrink-0">
        <div className="sticky top-0">
          <h1 className="mb-3 text-xl font-semibold">Documentation</h1>
          <nav className="space-y-1">
            {DOCS.map((d) => (
              <Link
                key={d.slug}
                to={`/docs/${d.slug}`}
                className={`block rounded-lg px-3 py-2 ${
                  d.slug === doc.slug
                    ? "bg-indigo-50 dark:bg-indigo-950"
                    : "hover:bg-slate-100 dark:hover:bg-slate-800"
                }`}
              >
                <div
                  className={`text-sm font-medium ${
                    d.slug === doc.slug ? "text-indigo-700 dark:text-indigo-300" : ""
                  }`}
                >
                  {d.title}
                </div>
                <div className="text-xs text-slate-400">{d.description}</div>
              </Link>
            ))}
          </nav>
          <a
            href={doc.slug === "overview" ? DOCS_SITE_URL : `${DOCS_SITE_URL}${doc.slug}.html`}
            target="_blank"
            rel="noreferrer"
            className="mt-4 block px-3 text-xs text-slate-400 hover:text-indigo-500 hover:underline"
          >
            View online ↗
          </a>
        </div>
      </aside>

      <article className="card min-w-0 max-w-3xl flex-1 px-8 py-6">
        <Markdown
          content={doc.content}
          onInternalLink={(route) => navigate(route)}
        />
      </article>
    </div>
  );
}

function Markdown({
  content,
  onInternalLink,
}: {
  content: string;
  onInternalLink: (route: string) => void;
}) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      components={{
        h1: ({ children }) => (
          <h1 className="mb-4 border-b border-slate-200 pb-2 text-2xl font-bold dark:border-slate-800">
            {children}
          </h1>
        ),
        h2: ({ children }) => (
          <h2 id={anchorId(children)} className="mb-3 mt-8 text-lg font-semibold">
            {children}
          </h2>
        ),
        h3: ({ children }) => (
          <h3 id={anchorId(children)} className="mb-2 mt-6 text-base font-semibold">
            {children}
          </h3>
        ),
        p: ({ children }) => (
          <p className="my-3 text-sm leading-6 text-slate-700 dark:text-slate-300">{children}</p>
        ),
        ul: ({ children }) => (
          <ul className="my-3 list-disc space-y-1 pl-6 text-sm text-slate-700 dark:text-slate-300">
            {children}
          </ul>
        ),
        ol: ({ children }) => (
          <ol className="my-3 list-decimal space-y-1 pl-6 text-sm text-slate-700 dark:text-slate-300">
            {children}
          </ol>
        ),
        li: ({ children }) => <li className="leading-6">{children}</li>,
        a: ({ href, children }) => {
          const route = href ? docRouteForHref(href) : null;
          if (route && route.startsWith("/")) {
            return (
              <a
                href={route}
                className="text-indigo-600 hover:underline dark:text-indigo-400"
                onClick={(e) => {
                  e.preventDefault();
                  onInternalLink(route);
                }}
              >
                {children}
              </a>
            );
          }
          return (
            <a
              href={href}
              target={href?.startsWith("http") ? "_blank" : undefined}
              rel="noreferrer"
              className="text-indigo-600 hover:underline dark:text-indigo-400"
            >
              {children}
            </a>
          );
        },
        code: ({ className, children }) => {
          const isBlock = /language-/.test(className ?? "") || String(children).includes("\n");
          if (isBlock) {
            return (
              <code className="block overflow-x-auto rounded-lg bg-slate-950 p-3 font-mono text-xs leading-5 text-slate-200">
                {children}
              </code>
            );
          }
          return (
            <code className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-xs text-slate-800 dark:bg-slate-800 dark:text-slate-200">
              {children}
            </code>
          );
        },
        pre: ({ children }) => <pre className="my-3">{children}</pre>,
        table: ({ children }) => (
          <div className="my-4 overflow-x-auto">
            <table className="w-full border-collapse text-sm">{children}</table>
          </div>
        ),
        th: ({ children }) => (
          <th className="border-b-2 border-slate-200 px-3 py-2 text-left text-xs font-semibold uppercase tracking-wide text-slate-500 dark:border-slate-700">
            {children}
          </th>
        ),
        td: ({ children }) => (
          <td className="border-b border-slate-100 px-3 py-2 align-top text-slate-700 dark:border-slate-800 dark:text-slate-300">
            {children}
          </td>
        ),
        blockquote: ({ children }) => (
          <blockquote className="my-3 border-l-4 border-indigo-300 bg-indigo-50/50 py-1 pl-4 text-sm italic dark:border-indigo-700 dark:bg-indigo-950/30">
            {children}
          </blockquote>
        ),
        hr: () => <hr className="my-6 border-slate-200 dark:border-slate-800" />,
      }}
    >
      {content}
    </ReactMarkdown>
  );
}

// anchorId reproduces GitHub-style heading anchors so intra-doc links
// (#docker-mode-with-kindminikube) resolve.
function anchorId(children: React.ReactNode): string {
  return String(flattenText(children))
    .toLowerCase()
    .replace(/[^\w\s-]/g, "")
    .trim()
    .replace(/\s+/g, "-");
}

function flattenText(node: React.ReactNode): string {
  if (node == null || typeof node === "boolean") return "";
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(flattenText).join("");
  if (typeof node === "object" && "props" in node) {
    return flattenText((node as { props: { children?: React.ReactNode } }).props.children);
  }
  return "";
}
