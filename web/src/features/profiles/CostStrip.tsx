/**
 * The cost strip above the editor's tabs: what the draft sends every request
 * before the conversation, each part a button to its tab.
 */

import type { Configuration } from "@/api/types";
import { toolsTokens } from "@/features/profiles/form";
import type { Draft, EditorSection, ToolGroups } from "@/features/profiles/form";
import type { EditorKind } from "@/features/profiles/SettingsEditor";
import { formatTokens } from "@/lib/format";
import { estimateTokens } from "@/lib/tokens";

type CostStripProps = {
  draft: Draft;
  inherited: Configuration;
  kind: EditorKind;
  groups: ToolGroups | undefined;
  onOpen: (section: EditorSection) => void;
};

/**
 * CostStrip says what the draft sends every request before the conversation:
 * the system prompt and the tool definitions, each a button to its tab.
 */
export function CostStrip({ draft, inherited, kind, groups, onOpen }: CostStripProps) {
  const base =
    kind === "chat"
      ? (draft.chat_prompt ?? inherited.chat_prompt)
      : (draft.workspace_prompt ?? inherited.workspace_prompt);
  const instructions = draft.instructions ?? inherited.instructions;
  const prompt = estimateTokens(base.trim()) + estimateTokens(instructions.trim());
  const toolCost = groups === undefined ? 0 : toolsTokens(groups, draft.tools ?? inherited.tools);
  const total = prompt + toolCost;
  const share = (n: number) => (total <= 0 ? 0 : (n / total) * 100);
  return (
    <div className="bg-muted/30 space-y-1.5 rounded-md border px-2.5 py-2">
      <p className="flex flex-wrap items-baseline gap-x-1.5 text-xs">
        <span className="font-mono text-sm font-semibold tabular-nums">~{formatTokens(total)}</span>
        <span className="text-muted-foreground">
          tokens every request sends before the conversation
          {kind === "chat" ? "" : ", not counting context files"}
          {kind === "profile" ? ", in a workspace session" : ""}
        </span>
      </p>
      <div className="bg-muted flex h-1.5 overflow-hidden rounded-full" aria-hidden>
        <span className="bg-chart-1 h-full" style={{ width: `${String(share(prompt))}%` }} />
        <span className="bg-chart-5 h-full" style={{ width: `${String(share(toolCost))}%` }} />
      </div>
      <div className="flex flex-wrap gap-x-3 text-xs">
        <CostPart
          color="bg-chart-1"
          label="System prompt"
          tokens={prompt}
          onClick={() => {
            onOpen("prompt");
          }}
        />
        <CostPart
          color="bg-chart-5"
          label="Tools"
          tokens={toolCost}
          onClick={() => {
            onOpen("tools");
          }}
        />
      </div>
    </div>
  );
}

type CostPartProps = { color: string; label: string; tokens: number; onClick: () => void };

function CostPart({ color, label, tokens, onClick }: CostPartProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="hover:bg-accent focus-visible:ring-ring -mx-1 flex items-center gap-1.5 rounded-md px-1 transition-colors focus-visible:ring-1 focus-visible:outline-none"
    >
      <span aria-hidden className={`${color} size-2 rounded-full`} />
      {label}
      <span className="text-muted-foreground font-mono tabular-nums">~{formatTokens(tokens)}</span>
    </button>
  );
}
