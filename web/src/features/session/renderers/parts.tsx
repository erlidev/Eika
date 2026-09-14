/**
 * The presentational pieces the built-in tool renderers share: a call's
 * output and the dense key/value header above it.
 */

import { OutputBlock } from "@/components/OutputBlock";
import type { ToolItem } from "@/features/session/transcript";

/** callOutput is what a call has produced: its stream while it runs, its content after. */
function callOutput(call: ToolItem): string {
  if (!call.done) return call.output;
  return call.output === "" ? (call.content ?? "") : call.output;
}

/** ResultBlock renders a call's output, or says it produced none. */
export function ResultBlock({ call, label }: { call: ToolItem; label: string }) {
  const text = callOutput(call);
  if (text === "") {
    return (
      <p className="text-muted-foreground font-mono text-xs">
        {call.done ? "no output" : "running…"}
      </p>
    );
  }
  return (
    <OutputBlock tone={call.isError ? "error" : "default"} label={label}>
      {text}
    </OutputBlock>
  );
}

/** FieldList is the dense key/value header several renderers share. */
export function FieldList({ fields }: { fields: [string, string][] }) {
  const shown = fields.filter(([, value]) => value !== "" && value !== "undefined");
  if (shown.length === 0) return null;
  return (
    <dl className="text-muted-foreground grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 font-mono text-xs">
      {shown.map(([name, value]) => (
        <div key={name} className="contents">
          <dt className="text-right">{name}</dt>
          <dd className="text-foreground truncate">{value}</dd>
        </div>
      ))}
    </dl>
  );
}
