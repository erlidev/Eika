/**
 * Monaco, set up once: its web workers bundled by Vite through `?worker`
 * imports (no CDN loader), and one theme per scheme built from the design
 * tokens. Only `CodeEditor.tsx` imports this, and the panel loads that
 * lazily, so the editor stays out of the main bundle.
 */

import * as monaco from "monaco-editor";
import EditorWorker from "monaco-editor/editor/editor.worker?worker";
import CssWorker from "monaco-editor/language/css/css.worker?worker";
import HtmlWorker from "monaco-editor/language/html/html.worker?worker";
import JsonWorker from "monaco-editor/language/json/json.worker?worker";
import TsWorker from "monaco-editor/language/typescript/ts.worker?worker";

import { themeColor } from "@/lib/themeColor";

globalThis.MonacoEnvironment = {
  getWorker(_workerId: string, label: string): Worker {
    switch (label) {
      case "json":
        return new JsonWorker();
      case "css":
      case "scss":
      case "less":
        return new CssWorker();
      case "html":
      case "handlebars":
      case "razor":
        return new HtmlWorker();
      case "typescript":
      case "javascript":
        return new TsWorker();
      default:
        return new EditorWorker();
    }
  },
};

/** hex drops the "#" Monaco's token rules do without. */
function bare(color: string): string {
  return color.slice(1);
}

/**
 * defineTheme registers the theme for the scheme in force from the current
 * tokens and returns its name. Called again after the scheme changes.
 */
export function defineTheme(dark: boolean): string {
  const name = dark ? "eika-dark" : "eika-light";
  monaco.editor.defineTheme(name, {
    base: dark ? "vs-dark" : "vs",
    inherit: true,
    rules: [
      { token: "comment", foreground: bare(themeColor("code-comment")), fontStyle: "italic" },
      { token: "keyword", foreground: bare(themeColor("code-keyword")) },
      { token: "string", foreground: bare(themeColor("code-string")) },
      { token: "number", foreground: bare(themeColor("code-literal")) },
      { token: "type", foreground: bare(themeColor("code-name")) },
    ],
    colors: {
      "editor.background": themeColor("background"),
      "editor.foreground": themeColor("foreground"),
      "editorLineNumber.foreground": themeColor("muted-foreground"),
      "editorLineNumber.activeForeground": themeColor("foreground"),
      "editor.selectionBackground": themeColor("accent"),
      "editor.lineHighlightBackground": themeColor("muted"),
      "editorCursor.foreground": themeColor("primary"),
      "editorWidget.background": themeColor("popover"),
      "editorWidget.border": themeColor("border"),
      focusBorder: themeColor("ring"),
    },
  });
  return name;
}

export { monaco };
