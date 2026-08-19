// The YAML editor, in Monaco — the same editor the web console uses.
//
// It was a <textarea>: no highlighting, no line numbers, and re-created on every paint
// because paint() rebuilds the DOM. Monaco is created ONCE and kept, for the same
// reason the React root and the graph host are: an editor that is thrown away on every
// keystroke loses the cursor, the scroll position, the undo history and the selection —
// everything that makes it an editor rather than a text box.
//
// Only the model is swapped when the file changes, so each file keeps its own undo
// stack and its own scroll position, which is what a tab in any editor does.

import * as monaco from "monaco-editor";
import editorWorker from "monaco-editor/editor/editor.worker?worker";

// No language workers: YAML has no Monaco language service, only a tokenizer, so the
// base worker is all that is ever asked for. Anything else would be shipping a
// TypeScript compiler to colour a config file.
self.MonacoEnvironment = { getWorker: () => new editorWorker() };

const host = document.createElement("div");
host.className = "editor";

let editor: monaco.editor.IStandaloneCodeEditor | null = null;
const models = new Map<string, monaco.editor.ITextModel>();
let onEdit: (text: string) => void = () => {};

export function editorHost(): HTMLElement {
  return host;
}

/** Called with the new text on every keystroke. */
export function onEditorChange(fn: (text: string) => void): void {
  onEdit = fn;
}

function theme(): string {
  return matchMedia("(prefers-color-scheme: dark)").matches ? "vs-dark" : "vs";
}

export function showFile(path: string, text: string): void {
  if (!editor) {
    editor = monaco.editor.create(host, {
      theme: theme(),
      language: "yaml",
      automaticLayout: true,
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
      fontSize: 12.5,
      lineNumbersMinChars: 3,
      renderLineHighlight: "none",
      tabSize: 4,
      insertSpaces: true,
      padding: { top: 8 },
    });
    editor.onDidChangeModelContent(() => onEdit(editor!.getValue()));
    matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () =>
      monaco.editor.setTheme(theme()),
    );
  }

  let model = models.get(path);
  if (!model) {
    model = monaco.editor.createModel(text, "yaml", monaco.Uri.parse("inmemory:/" + path));
    models.set(path, model);
  } else if (model.getValue() !== text) {
    // The file changed underneath us — not from typing, since that is where `text`
    // came from. Replacing the whole value keeps undo working.
    model.setValue(text);
  }
  if (editor.getModel() !== model) editor.setModel(model);
}
