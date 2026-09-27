/**
 * MarkdownField is a textarea that shows the markdown typed into it as it is
 * typed: markers quieter, strong text heavier, code coloured as the
 * transcript colours it. The text stays the source, so editing is a
 * textarea's in every way: the caret, selection, undo, spellcheck, and input
 * methods are the browser's own.
 *
 * It does that by drawing the text twice. The textarea is on top with its
 * text transparent and only its caret and selection showing; under it, a
 * copy of the text in the same box and type is split into styled runs. The
 * two stay in step only while every character takes the same space in both,
 * so a style here may change colour, background, decoration, stroke, or a
 * synthesised slant, and never size, weight, family, or spacing.
 *
 * In `markdown` mode it also edits as a markdown editor does, through
 * `lib/markdownEditing.ts`, and sends on Ctrl+Enter; in `chat` mode Enter
 * sends.
 */

import { useMemo, useRef } from "react";

import { Textarea } from "@/components/ui/textarea";
import type { ComposerMode } from "@/features/session/preferences";
import { closeFence, indent, newline } from "@/lib/markdownEditing";
import type { TextEdit } from "@/lib/markdownEditing";
import { markdownSource } from "@/lib/markdownSource";
import type { SourceKind } from "@/lib/markdownSource";
import { cn } from "@/lib/utils";

export type MarkdownFieldProps = Omit<React.ComponentProps<"textarea">, "value"> & {
  value: string;
  mode: ComposerMode;
  /** onSend is called for the key that sends in this mode. */
  onSend: () => void;
};

/**
 * box is what both layers share and must agree on to the pixel: padding,
 * type, wrapping, and a scrollbar gutter kept whether or not the textarea
 * scrolls, so that a scrollbar appearing does not rewrap only one of them.
 */
const box = "px-2.5 py-2 text-sm whitespace-pre-wrap break-words [scrollbar-gutter:stable]";

/**
 * style is the look of each kind. Strong text and headings are heavier by a
 * stroke rather than by weight, which would widen them. Italic is the
 * browser's slant of the upright face, which keeps its widths: no italic
 * face is loaded, and the composer spec checks that no style moves a
 * character. A fenced block's colours are its tokens'.
 */
const style: Record<SourceKind, string> = {
  marker: "text-muted-foreground",
  heading: "text-foreground [-webkit-text-stroke:0.4px_currentColor]",
  strong: "[-webkit-text-stroke:0.4px_currentColor]",
  emphasis: "italic",
  strike: "line-through",
  code: "bg-muted text-foreground rounded-md",
  block: "",
  link: "text-primary underline underline-offset-2",
  quote: "text-muted-foreground",
  bullet: "text-primary",
};

/**
 * apply makes an edit as if it were typed, so the browser's undo takes it
 * back. Setting the value would work too and would empty the undo history.
 */
function apply(box: HTMLTextAreaElement, edit: TextEdit) {
  box.setSelectionRange(edit.start, edit.end);
  const exec: (command: string, ui?: boolean, value?: string) => boolean =
    // eslint-disable-next-line @typescript-eslint/no-deprecated -- Nothing else edits a textarea in a way its undo history records.
    document.execCommand.bind(document);
  const typed =
    edit.text === ""
      ? edit.start === edit.end || exec("delete")
      : exec("insertText", false, edit.text);
  if (!typed) {
    // A browser without the command still gets the edit, without its undo.
    box.setRangeText(edit.text, edit.start, edit.end, "end");
    box.dispatchEvent(new Event("input", { bubbles: true }));
  }
  if (edit.selection !== undefined) box.setSelectionRange(...edit.selection);
}

export function MarkdownField({
  value,
  mode,
  onSend,
  className,
  disabled,
  onScroll,
  ...props
}: MarkdownFieldProps) {
  const mirror = useRef<HTMLDivElement>(null);
  const spans = useMemo(() => markdownSource(value), [value]);

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    // An input method ends its composition with Enter. Acting on it would
    // swallow the word the user was still typing.
    if (e.nativeEvent.isComposing) return;
    const el = e.currentTarget;
    const { value: text, selectionStart: start, selectionEnd: end } = el;
    let edit: TextEdit | null = null;
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      onSend();
      return;
    }
    if (mode === "chat") {
      if (e.key === "Enter" && !e.shiftKey && !e.altKey) {
        e.preventDefault();
        onSend();
      }
      return;
    }
    if (e.key === "Enter" && !e.shiftKey && !e.altKey) edit = newline(text, start, end);
    if (e.key === "Tab" && !e.ctrlKey && !e.metaKey && !e.altKey) {
      edit = indent(text, start, end, e.shiftKey);
    }
    if (edit === null) return;
    e.preventDefault();
    apply(el, edit);
  };

  return (
    <div className="relative">
      <div
        ref={mirror}
        aria-hidden
        className={cn(
          box,
          "text-foreground pointer-events-none absolute inset-0 overflow-hidden",
          // A markdown block's strong text is bold in the transcript's theme;
          // here it is the stroke, like any strong text.
          "[&_.hljs-strong]:font-normal [&_.hljs-strong]:[-webkit-text-stroke:0.4px_currentColor]",
          disabled && "opacity-50",
        )}
      >
        {spans.map((span, i) => (
          <span
            key={i}
            className={cn(
              span.kinds.map((k) => style[k]),
              span.token,
            )}
          >
            {span.text}
          </span>
        ))}
        {/* A textarea gives a trailing newline a line of its own; a div drops it. */}{" "}
      </div>
      <Textarea
        {...props}
        value={value}
        disabled={disabled}
        className={cn(
          box,
          "caret-foreground selection:bg-primary/25 relative text-transparent selection:text-transparent",
          className,
        )}
        onKeyDown={onKeyDown}
        onInput={(e) => {
          const native = e.nativeEvent;
          if (mode !== "markdown" || native.inputType !== "insertText" || native.data !== "`") {
            return;
          }
          const el = e.currentTarget;
          const edit = closeFence(el.value, el.selectionStart);
          if (edit !== null) apply(el, edit);
        }}
        onScroll={(e) => {
          if (mirror.current) mirror.current.scrollTop = e.currentTarget.scrollTop;
          onScroll?.(e);
        }}
      />
    </div>
  );
}
