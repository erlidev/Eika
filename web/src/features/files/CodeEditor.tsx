/**
 * The Monaco editor the Files panel shows. It shows one file at a time, picks
 * its language from the path, and reports every change and every Ctrl/Cmd+S
 * to its owner, which decides what is saved.
 */

import { useEffect, useRef, useSyncExternalStore } from "react";

import { resolvedTheme, subscribeTheme } from "@/app/theme";
import { defineTheme, monaco } from "@/features/files/monaco";

export type CodeEditorProps = {
  path: string;
  /** value is the text to show; a change from outside replaces the editor's text. */
  value: string;
  onChange: (text: string) => void;
  onSave: () => void;
};

export function CodeEditor({ path, value, onChange, onSave }: CodeEditorProps) {
  const container = useRef<HTMLDivElement>(null);
  const editor = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  const handlers = useRef({ onChange, onSave });
  const theme = useSyncExternalStore(subscribeTheme, resolvedTheme, () => "light" as const);

  useEffect(() => {
    handlers.current = { onChange, onSave };
  });

  useEffect(() => {
    const element = container.current;
    if (!element) return;
    const instance = monaco.editor.create(element, {
      automaticLayout: true,
      fontFamily: '"JetBrains Mono Variable", ui-monospace, monospace',
      fontSize: 12,
      lineHeight: 18,
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      renderLineHighlight: "line",
      cursorBlinking: "solid",
      tabSize: 4,
      theme: defineTheme(resolvedTheme() === "dark"),
      ariaLabel: "File contents",
    });
    // Monaco measured the font as it was when the editor opened.
    void document.fonts.ready.then(() => {
      monaco.editor.remeasureFonts();
    });
    instance.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => {
      handlers.current.onSave();
    });
    const changes = instance.onDidChangeModelContent(() => {
      handlers.current.onChange(instance.getValue());
    });
    editor.current = instance;
    return () => {
      changes.dispose();
      instance.getModel()?.dispose();
      instance.dispose();
      editor.current = null;
    };
  }, []);

  useEffect(() => {
    const instance = editor.current;
    if (!instance) return;
    const uri = monaco.Uri.file(path);
    let model = monaco.editor.getModel(uri);
    if (!model) {
      model = monaco.editor.createModel(value, undefined, uri);
    } else if (model.getValue() !== value) {
      // Keeps the undo stack, unlike setValue.
      model.pushEditOperations([], [{ range: model.getFullModelRange(), text: value }], () => null);
    }
    if (instance.getModel() !== model) {
      const previous = instance.getModel();
      instance.setModel(model);
      previous?.dispose();
    }
  }, [path, value]);

  useEffect(() => {
    monaco.editor.setTheme(defineTheme(theme === "dark"));
  }, [theme]);

  return <div ref={container} className="h-full min-h-0 w-full" />;
}
