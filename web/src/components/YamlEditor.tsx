import { useEffect, useState } from "react";
import Editor from "@monaco-editor/react";
import "../monaco";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import { ErrorBox } from "./ui";

// YamlEditor shows an editable manifest with save/reset. On save it PUTs the
// YAML back; API validation errors (bad YAML, immutable fields, RBAC) are
// shown inline.
export default function YamlEditor({
  kind,
  namespace,
  name,
  initialYaml,
  dark,
  readOnly = false,
}: {
  kind: string;
  namespace: string;
  name: string;
  initialYaml: string;
  dark: boolean;
  readOnly?: boolean;
}) {
  const [value, setValue] = useState(initialYaml);
  const [saved, setSaved] = useState(false);
  const queryClient = useQueryClient();

  useEffect(() => setValue(initialYaml), [initialYaml]);

  const mutation = useMutation({
    mutationFn: (yaml: string) => api.updateResource(kind, namespace, name, yaml),
    onSuccess: (detail) => {
      setValue(detail.yaml);
      setSaved(true);
      setTimeout(() => setSaved(false), 2500);
      queryClient.invalidateQueries({ queryKey: ["resource", kind, namespace, name] });
      queryClient.invalidateQueries({ queryKey: ["resources", kind] });
    },
  });

  const dirty = value !== initialYaml && !mutation.isSuccess;

  return (
    <div>
      {readOnly ? (
        <div className="mb-3 text-sm text-slate-500 dark:text-slate-400">
          Read-only mode — manifests can be viewed but not edited. Redeploy with
          mode=assisted to enable editing.
        </div>
      ) : (
        <div className="mb-3 flex items-center gap-2">
          <button
            className="btn-primary"
            disabled={mutation.isPending || value === initialYaml}
            onClick={() => mutation.mutate(value)}
          >
            {mutation.isPending ? "Applying..." : "Apply changes"}
          </button>
          <button
            className="btn-ghost"
            disabled={!dirty}
            onClick={() => {
              setValue(initialYaml);
              mutation.reset();
            }}
          >
            Reset
          </button>
          {saved && <span className="text-sm text-emerald-600">✓ Applied</span>}
        </div>
      )}

      {mutation.error != null && (
        <div className="mb-3">
          <ErrorBox title="Apply failed" message={(mutation.error as Error).message} />
        </div>
      )}

      <div className="overflow-hidden rounded-lg border border-slate-200 dark:border-slate-800">
        <Editor
          height="60vh"
          language="yaml"
          theme={dark ? "vs-dark" : "light"}
          value={value}
          onChange={(v) => setValue(v ?? "")}
          options={{
            minimap: { enabled: false },
            fontSize: 13,
            scrollBeyondLastLine: false,
            wordWrap: "on",
            readOnly,
          }}
        />
      </div>
    </div>
  );
}
