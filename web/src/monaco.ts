// Bundle Monaco locally instead of loading it from a CDN, so the UI works in
// air-gapped clusters. YAML editing only needs the core editor worker.
import * as monaco from "monaco-editor";
import editorWorker from "monaco-editor/esm/vs/editor/editor.worker?worker";
import { loader } from "@monaco-editor/react";

self.MonacoEnvironment = {
  getWorker() {
    return new editorWorker();
  },
};

loader.config({ monaco });
