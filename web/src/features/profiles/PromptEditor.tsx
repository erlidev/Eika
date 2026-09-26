/**
 * A text of the system prompt a layer can replace: a base prompt or the extra
 * instructions. Unset, it shows the text it falls through to; Override starts
 * from that text, so a small change needs no copying. A base prompt that
 * differs from the built-in one can show the difference line by line.
 */

import { GitCompare } from "lucide-react";
import { useState } from "react";

import type { ConfigLayer } from "@/api/types";
import { DiffRows } from "@/components/DiffRows";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Inherited, LayerChip, ResetButton } from "@/features/profiles/fields";
import { promptChange } from "@/features/profiles/form";
import { formatTokens } from "@/lib/format";
import { lineDiff } from "@/lib/lineDiff";
import { estimateTokens } from "@/lib/tokens";

export type PromptEditorProps = {
  id: string;
  label: string;
  hint?: string;
  /** value is the text set here, or null when the layer leaves it to fall through. */
  value: string | null;
  inherited: string;
  from: ConfigLayer;
  /** builtin is the built-in text a set prompt is compared with. */
  builtin?: string;
  onChange: (value: string | null) => void;
};

export function PromptEditor({
  id,
  label,
  hint,
  value,
  inherited,
  from,
  builtin,
  onChange,
}: PromptEditorProps) {
  const [comparing, setComparing] = useState(false);
  const text = value ?? inherited;
  const change = value !== null && builtin !== undefined ? promptChange(builtin, value) : null;
  const canCompare = change !== null && !change.same;
  const hintId = `${id}-hint`;

  return (
    <div className="overflow-hidden rounded-md border">
      <header className="bg-muted/40 flex min-h-8 flex-wrap items-center gap-x-2 gap-y-1 border-b px-2.5 py-1">
        <Label htmlFor={id} id={`${id}-label`}>
          {label}
        </Label>
        <LayerChip set={value !== null} from={from} />
        <span
          className="text-muted-foreground font-mono text-2xs tabular-nums"
          title="Estimated tokens"
        >
          ~{formatTokens(estimateTokens(text.trim()))}
        </span>
        <div className="ml-auto flex items-center gap-1">
          {canCompare && (
            <Button
              type="button"
              size="xs"
              variant={comparing ? "secondary" : "ghost"}
              aria-pressed={comparing}
              onClick={() => {
                setComparing(!comparing);
              }}
            >
              <GitCompare aria-hidden />
              Changes<span className="sr-only"> to the built-in {label.toLowerCase()}</span>
            </Button>
          )}
          {value === null ? (
            <Button
              type="button"
              size="xs"
              variant="outline"
              onClick={() => {
                onChange(inherited);
              }}
            >
              Override<span className="sr-only"> {label.toLowerCase()}</span>
            </Button>
          ) : (
            <ResetButton
              label={label.toLowerCase()}
              onClick={() => {
                setComparing(false);
                onChange(null);
              }}
            />
          )}
        </div>
      </header>
      {hint !== undefined && (
        <p id={hintId} className="text-muted-foreground border-b px-2.5 py-1 text-xs">
          {hint}
        </p>
      )}
      {value === null ? (
        <div
          id={id}
          className="text-muted-foreground max-h-40 overflow-y-auto px-2.5 py-2 font-mono text-xs leading-relaxed whitespace-pre-wrap"
        >
          {inherited === "" ? <span className="font-sans italic">None</span> : inherited}
        </div>
      ) : comparing && builtin !== undefined ? (
        <div
          id={id}
          role="group"
          aria-label={`Changes to the built-in ${label.toLowerCase()}`}
          className="max-h-72 overflow-auto py-1 font-mono text-xs leading-relaxed"
        >
          <DiffRows lines={lineDiff(builtin, value)} lineNumber={(l) => l.newLine ?? l.oldLine} />
        </div>
      ) : (
        <Textarea
          id={id}
          value={value}
          rows={8}
          aria-describedby={hint === undefined ? undefined : hintId}
          className="max-h-96 min-h-32 border-0 font-mono text-xs leading-relaxed shadow-none focus-visible:ring-0"
          onChange={(e) => {
            onChange(e.target.value);
          }}
        />
      )}
      {value === null ? (
        <div className="border-t px-2.5 py-1">
          <Inherited from={from} />
        </div>
      ) : (
        change !== null && (
          <p className="text-muted-foreground border-t px-2.5 py-1 text-xs">
            {change.same ? (
              "The same as the built-in prompt."
            ) : (
              <>
                Differs from the built-in prompt:{" "}
                <span className="text-success font-mono">+{change.added}</span>{" "}
                <span className="text-destructive font-mono">-{change.removed}</span> lines.
              </>
            )}
            {value === "" && " An empty prompt sends no base prompt at all."}
          </p>
        )
      )}
    </div>
  );
}
