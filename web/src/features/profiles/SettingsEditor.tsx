/**
 * The editor of one configuration layer: a profile, or what a session sets
 * over its profile. Every field says where its value comes from, set here or
 * the layer it falls through from, and a set field has a reset that unsets
 * it. The Model, Prompt, Tools, and Sampling tabs hold the four kinds of
 * setting; above them, a strip says what the draft costs every request
 * before the conversation starts.
 */

import { useState } from "react";

import type { Configuration, Model } from "@/api/types";
import { LoadError, Notice } from "@/components/Notice";
import { SectionTabs } from "@/components/SectionTabs";
import { CostStrip } from "@/features/profiles/CostStrip";
import { ModelField, SwitchField, ToolsField } from "@/features/profiles/EditorFields";
import { problemSections, samplingFields, sourceOf, toolGroups } from "@/features/profiles/form";
import type { Draft, EditorSection, SamplingProblem } from "@/features/profiles/form";
import { PromptEditor } from "@/features/profiles/PromptEditor";
import { SamplingControl } from "@/features/profiles/SamplingControls";
import { useModels } from "@/features/providers";
import { useTools } from "@/features/session/queries";

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
