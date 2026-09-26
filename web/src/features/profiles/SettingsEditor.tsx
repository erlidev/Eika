/**
 * The editor of one configuration layer: a profile, or what a session sets
 * over its profile. Every field says where its value comes from, set here or
 * the layer it falls through from, and a set field has a reset that unsets
 * it. The Model, Prompt, Tools, and Sampling tabs hold the four kinds of
 * setting; above them, a strip says what the draft costs every request
 * before the conversation starts.
 */

import { useState } from "react";

import type { ConfigLayer, Configuration, Model } from "@/api/types";
import { LoadError, Notice } from "@/components/Notice";
import { SectionTabs } from "@/components/SectionTabs";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { ChoiceGroup, FieldHeader, Inherited, ResetButton } from "@/features/profiles/fields";
import {
  choiceSummary,
  everyTool,
  layerName,
  problemSections,
  samplingFields,
  sourceOf,
  toolGroups,
  toolsTokens,
} from "@/features/profiles/form";
import type { Draft, EditorSection, SamplingProblem, ToolGroups } from "@/features/profiles/form";
import { PromptEditor } from "@/features/profiles/PromptEditor";
import { SamplingControl } from "@/features/profiles/SamplingControls";
import { ToolPicker } from "@/features/profiles/ToolPicker";
import { useModels } from "@/features/providers";
import { useTools } from "@/features/session/queries";
import { formatTokens } from "@/lib/format";
import { estimateTokens } from "@/lib/tokens";

/** EditorKind says what the layer configures: a profile serves both kinds of session. */
export type EditorKind = "profile" | "workspace" | "chat";

export type SettingsEditorProps = {
  /** idPrefix keeps the field ids of two editors on one page apart. */
  idPrefix: string;
  draft: Draft;
  onChange: (draft: Draft) => void;
  /** inherited is what the layer's unset values fall through to. */
  inherited: Configuration;
  /** prompts are the built-in base prompts, which a set prompt is compared with. */
  prompts: { workspace: string; chat: string };
  kind: EditorKind;
  problems: readonly SamplingProblem[];
  /** initialSection is the tab the editor opens on. */
  initialSection?: EditorSection | undefined;
};

/** inheritSelect is the model select's value for "not set here". */
const inheritSelect = "__inherit";

const sectionTitles: Record<EditorSection, string> = {
  model: "Model",
  prompt: "Prompt",
  tools: "Tools",
  sampling: "Sampling",
};

export function SettingsEditor({
  idPrefix,
  draft,
  onChange,
  inherited,
  prompts,
  kind,
  problems,
  initialSection = "model",
}: SettingsEditorProps) {
  const set = (patch: Partial<Draft>) => {
    onChange({ ...draft, ...patch });
  };
  const id = (name: string) => `${idPrefix}-${name}`;
  const [section, setSection] = useState<EditorSection>(initialSection);
  const models = useModels();
  const tools = useTools();
  const modelList: Model[] = models.data?.models ?? [];
  const runs = modelList.find((m) => m.id === (draft.model_id || inherited.model_id));
  const groups = tools.data === undefined ? undefined : toolGroups(tools.data, kind === "chat");
  const counts = problemSections(problems);
  const panel = (name: EditorSection) => ({
    role: "tabpanel",
    id: `${idPrefix}-section-${name}`,
    "aria-labelledby": `${idPrefix}-tab-${name}`,
  });
  const samplingControl = (field: (typeof samplingFields)[number]) => (
    <SamplingControl
      key={field.key}
      id={id(`sampling-${field.key}`)}
      field={field}
      value={draft.sampling[field.key]}
      inherited={inherited}
      model={runs}
      problem={problems.find((p) => p.key === field.key)?.message}
      onChange={(text) => {
        set({ sampling: { ...draft.sampling, [field.key]: text } });
      }}
    />
  );

  return (
    <div className="space-y-3">
      <CostStrip
        draft={draft}
        inherited={inherited}
        kind={kind}
        groups={groups}
        onOpen={setSection}
      />
      <SectionTabs
        idPrefix={idPrefix}
        label="Settings"
        tabs={(["model", "prompt", "tools", "sampling"] as const).map((s) => ({
          id: s,
          title: sectionTitles[s],
          ...(counts[s] === undefined ? {} : { count: counts[s] }),
        }))}
        value={section}
        onChange={setSection}
      />

      {section === "model" && (
        <div {...panel("model")} className="space-y-5">
          <ModelField
            id={id("model")}
            draft={draft}
            inherited={inherited}
            models={modelList}
            runs={runs}
            onChange={set}
          />
          {models.isError && (
            <LoadError
              what="the models"
              error={models.error}
              retrying={models.isFetching}
              retry={() => void models.refetch()}
            />
          )}
          {inherited.dropped_effort !== undefined && inherited.dropped_effort !== "" && (
            <Notice>
              The reasoning effort <span className="font-mono">{inherited.dropped_effort}</span> is
              not one the model offers, so it is not sent.
            </Notice>
          )}
          {samplingFields.filter((f) => f.section === "model").map(samplingControl)}
          <SwitchField
            id={id("preserve-thinking")}
            label="Preserve thinking"
            hint="Send the model's earlier reasoning back with the conversation. It costs context; some models reason better across turns with it."
            options={[
              { value: "on", label: "Replay" },
              { value: "off", label: "Leave out" },
            ]}
            value={draft.preserve_thinking}
            inherited={inherited.preserve_thinking}
            from={sourceOf(inherited, "preserve_thinking")}
            onChange={(preserve_thinking) => {
              set({ preserve_thinking });
            }}
          />
        </div>
      )}

      {section === "prompt" && (
        <div {...panel("prompt")} className="space-y-4">
          {kind !== "chat" && (
            <PromptEditor
              id={id("workspace-prompt")}
              label="Base prompt of a workspace session"
              value={draft.workspace_prompt}
              inherited={inherited.workspace_prompt}
              from={sourceOf(inherited, "workspace_prompt")}
              builtin={prompts.workspace}
              onChange={(workspace_prompt) => {
                set({ workspace_prompt });
              }}
            />
          )}
          {kind !== "workspace" && (
            <PromptEditor
              id={id("chat-prompt")}
              label="Base prompt of a chat"
              value={draft.chat_prompt}
              inherited={inherited.chat_prompt}
              from={sourceOf(inherited, "chat_prompt")}
              builtin={prompts.chat}
              onChange={(chat_prompt) => {
                set({ chat_prompt });
              }}
            />
          )}
          {kind !== "chat" && (
            <SwitchField
              id={id("context-files")}
              label="Context files"
              hint="The workspace's AGENTS.md files, added to the system prompt after the base prompt."
              options={[
                { value: "on", label: "Read" },
                { value: "off", label: "Skip" },
              ]}
              value={draft.context_files}
              inherited={inherited.context_files}
              from={sourceOf(inherited, "context_files")}
              onChange={(context_files) => {
                set({ context_files });
              }}
            />
          )}
          <PromptEditor
            id={id("instructions")}
            label="Extra instructions"
            hint="Added last, after the base prompt and the context files."
            value={draft.instructions}
            inherited={inherited.instructions}
            from={sourceOf(inherited, "instructions")}
            onChange={(instructions) => {
              set({ instructions });
            }}
          />
        </div>
      )}

      {section === "tools" && (
        <div {...panel("tools")}>
          {tools.isPending && <Notice tone="pending">Loading the tools…</Notice>}
          {tools.isError && (
            <LoadError
              what="the tools"
              error={tools.error}
              retrying={tools.isFetching}
              retry={() => void tools.refetch()}
            />
          )}
          {groups !== undefined && (
            <ToolsField
              idPrefix={id("tool")}
              value={draft.tools}
              inherited={inherited}
              groups={groups}
              onChange={(next) => {
                set({ tools: next });
              }}
            />
          )}
        </div>
      )}

      {section === "sampling" && (
        <div {...panel("sampling")} className="space-y-4">
          <p className="text-muted-foreground text-xs">
            A parameter left unset falls through to the value its input shows; one no layer sets is
            left to the endpoint.
          </p>
          <div className="grid gap-x-6 gap-y-4 sm:grid-cols-2">
            {samplingFields.filter((f) => f.section === "sampling").map(samplingControl)}
          </div>
        </div>
      )}
    </div>
  );
}

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
function CostStrip({ draft, inherited, kind, groups, onOpen }: CostStripProps) {
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

type ModelFieldProps = {
  id: string;
  draft: Draft;
  inherited: Configuration;
  models: readonly Model[];
  /** runs is the model the layer runs: its own choice, or the one it inherits. */
  runs: Model | undefined;
  onChange: (patch: Partial<Draft>) => void;
};

function ModelField({ id, draft, inherited, models, runs, onChange }: ModelFieldProps) {
  const inheritedName = inherited.model === "" ? "no model" : inherited.model;
  return (
    <div className="space-y-1.5">
      <FieldHeader
        htmlFor={id}
        label="Model"
        set={draft.model_id !== ""}
        from={sourceOf(inherited, "model")}
      >
        {draft.model_id !== "" && (
          <ResetButton
            label="the model"
            onClick={() => {
              onChange({ model_id: "" });
            }}
          />
        )}
      </FieldHeader>
      <Select
        value={draft.model_id === "" ? inheritSelect : draft.model_id}
        onValueChange={(value) => {
          onChange({ model_id: value === inheritSelect ? "" : value });
        }}
      >
        <SelectTrigger id={id} className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={inheritSelect}>
            <span className="text-muted-foreground">
              {inheritedName}, from {layerName(sourceOf(inherited, "model"))}
            </span>
          </SelectItem>
          {models.map((m) => (
            <SelectItem key={m.id} value={m.id}>
              {m.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {runs !== undefined && (
        <dl className="text-muted-foreground flex flex-wrap gap-x-4 gap-y-0.5 text-xs">
          <div className="flex gap-1">
            <dt>Endpoint id</dt>
            <dd className="text-foreground font-mono">{runs.model}</dd>
          </div>
          <div className="flex gap-1">
            <dt>Context window</dt>
            <dd className="text-foreground font-mono tabular-nums">
              {formatTokens(runs.context_window)}
            </dd>
          </div>
          <div className="flex gap-1">
            <dt>Max output</dt>
            <dd className="text-foreground font-mono tabular-nums">
              {formatTokens(runs.max_output)}
            </dd>
          </div>
        </dl>
      )}
    </div>
  );
}

type SwitchFieldProps = {
  id: string;
  label: string;
  hint: string;
  options: readonly [{ value: "on"; label: string }, { value: "off"; label: string }];
  value: boolean | null;
  inherited: boolean;
  from: ConfigLayer;
  onChange: (value: boolean | null) => void;
};

/** SwitchField is a setting that is on or off, or left to fall through. */
function SwitchField({
  id,
  label,
  hint,
  options,
  value,
  inherited,
  from,
  onChange,
}: SwitchFieldProps) {
  const labelId = `${id}-label`;
  const fallback = options.find((o) => o.value === (inherited ? "on" : "off"))?.label ?? "";
  return (
    <div className="space-y-1.5">
      <FieldHeader id={labelId} label={label} set={value !== null} from={from}>
        {value !== null && (
          <ResetButton
            label={label.toLowerCase()}
            onClick={() => {
              onChange(null);
            }}
          />
        )}
      </FieldHeader>
      <ChoiceGroup
        labelledBy={labelId}
        options={options}
        value={value === null ? null : value ? "on" : "off"}
        inherited={inherited ? "on" : "off"}
        onChange={(next) => {
          onChange(next === null ? null : next === "on");
        }}
      />
      <p className="text-muted-foreground text-xs">
        {hint}
        {value === null && ` Not set here: ${fallback.toLowerCase()}, from ${layerName(from)}.`}
      </p>
    </div>
  );
}

type ToolsFieldProps = {
  idPrefix: string;
  value: string[] | null;
  inherited: Configuration;
  groups: ToolGroups;
  onChange: (value: string[] | null) => void;
};

/**
 * ToolsField is the layer's tool choice: none, which falls through and shows
 * the choice it falls through to, or a list of tools and whole MCP servers.
 */
function ToolsField({ idPrefix, value, inherited, groups, onChange }: ToolsFieldProps) {
  const from = sourceOf(inherited, "tools");
  const below = inherited.tools ?? everyTool(groups);
  return (
    <div className="space-y-3">
      <FieldHeader label="Tools" set={value !== null} from={from}>
        {value === null ? (
          <Button
            type="button"
            size="xs"
            variant="outline"
            onClick={() => {
              onChange([...below]);
            }}
          >
            Choose the tools here
          </Button>
        ) : (
          <ResetButton
            label="the tools"
            onClick={() => {
              onChange(null);
            }}
          />
        )}
      </FieldHeader>
      {value === null && <Inherited from={from}>{choiceSummary(inherited.tools)}</Inherited>}
      <ToolPicker
        idPrefix={idPrefix}
        groups={groups}
        choice={value ?? below}
        readOnly={value === null}
        onChange={onChange}
      />
    </div>
  );
}
