/**
 * The editor of one configuration layer: a profile, or what a session sets
 * over its profile. Every field shows what it falls through to, muted, while
 * the layer leaves it unset, and a set field has a reset that unsets it. The
 * Prompt, Tools, and Sampling tabs hold the three kinds of setting.
 */

import { RotateCcw } from "lucide-react";
import { useState } from "react";

import type { Configuration, ConfigLayer, Model, Tool } from "@/api/types";
import { LoadError, Notice } from "@/components/Notice";
import { SectionTabs } from "@/components/SectionTabs";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import {
  choiceSummary,
  chosen,
  everyTool,
  inheritedSampling,
  layerName,
  promptChange,
  samplingFields,
  serverEntry,
  sourceOf,
  toolGroups,
  withChoice,
} from "@/features/profiles/form";
import type { Draft, SamplingField, SamplingProblem } from "@/features/profiles/form";
import { useModels } from "@/features/providers";
import { toolSummary, useTools } from "@/features/session";

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
};

/** Section is one tab of the editor. */
type Section = "prompt" | "tools" | "sampling";

/** inheritSelect is the model select's value for "not set here". */
const inheritSelect = "__inherit";

export function SettingsEditor({
  idPrefix,
  draft,
  onChange,
  inherited,
  prompts,
  kind,
  problems,
}: SettingsEditorProps) {
  const set = (patch: Partial<Draft>) => {
    onChange({ ...draft, ...patch });
  };
  const id = (name: string) => `${idPrefix}-${name}`;
  const [section, setSection] = useState<Section>("prompt");
  const panel = (name: Section) => ({
    role: "tabpanel",
    id: `${idPrefix}-section-${name}`,
    "aria-labelledby": `${idPrefix}-tab-${name}`,
  });

  return (
    <div className="space-y-3">
      <SectionTabs
        idPrefix={idPrefix}
        label="Settings"
        tabs={[
          { id: "prompt", title: "Prompt" },
          { id: "tools", title: "Tools" },
          {
            id: "sampling",
            title: "Sampling",
            ...(problems.length > 0 ? { count: problems.length } : {}),
          },
        ]}
        value={section}
        onChange={setSection}
      />

      {section === "prompt" && (
        <div {...panel("prompt")} className="space-y-4">
          <ModelField id={id("model")} draft={draft} inherited={inherited} onChange={set} />
          {kind !== "chat" && (
            <PromptField
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
            <PromptField
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
          <PromptField
            id={id("instructions")}
            label="Extra instructions"
            hint="Follow the base prompt and the context files."
            value={draft.instructions}
            inherited={inherited.instructions}
            from={sourceOf(inherited, "instructions")}
            onChange={(instructions) => {
              set({ instructions });
            }}
          />
          {kind !== "chat" && (
            <ContextFilesField
              id={id("context-files")}
              value={draft.context_files}
              inherited={inherited}
              onChange={(context_files) => {
                set({ context_files });
              }}
            />
          )}
        </div>
      )}

      {section === "tools" && (
        <div {...panel("tools")}>
          <ToolsField
            idPrefix={id("tool")}
            value={draft.tools}
            inherited={inherited}
            chat={kind === "chat"}
            onChange={(tools) => {
              set({ tools });
            }}
          />
        </div>
      )}

      {section === "sampling" && (
        <div {...panel("sampling")} className="space-y-3">
          <p className="text-muted-foreground text-xs">
            A parameter left empty falls through to the value shown in it. What no layer sets is
            left to the endpoint.
          </p>
          {inherited.dropped_effort !== undefined && inherited.dropped_effort !== "" && (
            <Notice>
              The reasoning effort <span className="font-mono">{inherited.dropped_effort}</span> is
              not one the model offers, so it is not sent.
            </Notice>
          )}
          <div className="grid gap-3 sm:grid-cols-2">
            {samplingFields.map((field) => (
              <SamplingInput
                key={field.key}
                id={id(`sampling-${field.key}`)}
                field={field}
                value={draft.sampling[field.key]}
                inherited={inheritedSampling(inherited, field.key)}
                problem={problems.find((p) => p.key === field.key)?.message}
                onChange={(text) => {
                  set({ sampling: { ...draft.sampling, [field.key]: text } });
                }}
              />
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

/** Inherited says, muted, what an unset field falls through to. */
function Inherited({ from, children }: { from: ConfigLayer; children?: React.ReactNode }) {
  return (
    <p className="text-muted-foreground text-xs">
      Not set here: {children ?? "falls through"} from {layerName(from)}.
    </p>
  );
}

/** ResetButton unsets a field, so that it falls through again. */
function ResetButton({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <Button type="button" size="xs" variant="ghost" onClick={onClick}>
      <RotateCcw aria-hidden />
      Reset<span className="sr-only"> {label}</span>
    </Button>
  );
}

type ModelFieldProps = {
  id: string;
  draft: Draft;
  inherited: Configuration;
  onChange: (patch: Partial<Draft>) => void;
};

function ModelField({ id, draft, inherited, onChange }: ModelFieldProps) {
  const models = useModels();
  const list: Model[] = models.data?.models ?? [];
  const inheritedName = inherited.model === "" ? "no model" : inherited.model;
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between gap-2">
        <Label htmlFor={id}>Model</Label>
        {draft.model_id !== "" && (
          <ResetButton
            label="the model"
            onClick={() => {
              onChange({ model_id: "" });
            }}
          />
        )}
      </div>
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
          {list.map((m) => (
            <SelectItem key={m.id} value={m.id}>
              {m.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {models.isError && (
        <LoadError
          what="the models"
          error={models.error}
          retrying={models.isFetching}
          retry={() => void models.refetch()}
        />
      )}
    </div>
  );
}

type PromptFieldProps = {
  id: string;
  label: string;
  hint?: string;
  value: string | null;
  inherited: string;
  from: ConfigLayer;
  /** builtin is the built-in text a set prompt is compared with. */
  builtin?: string;
  onChange: (value: string | null) => void;
};

/**
 * PromptField is a text the layer can replace. Unset, it shows the text it
 * falls through to; Override starts from that text, so a small change to
 * the built-in prompt needs no copying.
 */
function PromptField({
  id,
  label,
  hint,
  value,
  inherited,
  from,
  builtin,
  onChange,
}: PromptFieldProps) {
  const change = value !== null && builtin !== undefined ? promptChange(builtin, value) : null;
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between gap-2">
        <Label htmlFor={id}>{label}</Label>
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
              onChange(null);
            }}
          />
        )}
      </div>
      {hint !== undefined && <p className="text-muted-foreground text-xs">{hint}</p>}
      {value === null ? (
        <>
          <div
            id={id}
            className="bg-muted/50 text-muted-foreground max-h-32 overflow-y-auto rounded-md border px-2.5 py-2 font-mono text-xs whitespace-pre-wrap"
          >
            {inherited === "" ? <span className="font-sans italic">None</span> : inherited}
          </div>
          <Inherited from={from} />
        </>
      ) : (
        <>
          <Textarea
            id={id}
            value={value}
            rows={6}
            className="max-h-72 font-mono text-xs"
            onChange={(e) => {
              onChange(e.target.value);
            }}
          />
          {change !== null && (
            <p className="text-muted-foreground text-xs">
              {change.same
                ? "The same as the built-in prompt."
                : `Differs from the built-in prompt: ${String(change.added)} lines added, ${String(change.removed)} removed.`}
              {value === "" && " An empty prompt sends no base prompt at all."}
            </p>
          )}
        </>
      )}
    </div>
  );
}

type ContextFilesFieldProps = {
  id: string;
  value: boolean | null;
  inherited: Configuration;
  onChange: (value: boolean | null) => void;
};

function ContextFilesField({ id, value, inherited, onChange }: ContextFilesFieldProps) {
  const fallback = inherited.context_files ? "read" : "not read";
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>Context files</Label>
      <Select
        value={value === null ? inheritSelect : value ? "on" : "off"}
        onValueChange={(next) => {
          onChange(next === inheritSelect ? null : next === "on");
        }}
      >
        <SelectTrigger id={id} className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={inheritSelect}>
            <span className="text-muted-foreground">
              {fallback}, from {layerName(sourceOf(inherited, "context_files"))}
            </span>
          </SelectItem>
          <SelectItem value="on">Read the workspace&apos;s AGENTS.md files</SelectItem>
          <SelectItem value="off">Do not read them</SelectItem>
        </SelectContent>
      </Select>
    </div>
  );
}

type ToolsFieldProps = {
  idPrefix: string;
  value: string[] | null;
  inherited: Configuration;
  chat: boolean;
  onChange: (value: string[] | null) => void;
};

/**
 * ToolsField is the layer's tool choice: nothing, which falls through, or a
 * list of tools and whole MCP servers. A server that is chosen whole keeps
 * the tools it adds later.
 */
function ToolsField({ idPrefix, value, inherited, chat, onChange }: ToolsFieldProps) {
  const tools = useTools();
  if (tools.isPending) return <Notice tone="pending">Loading the tools…</Notice>;
  if (tools.isError) {
    return (
      <LoadError
        what="the tools"
        error={tools.error}
        retrying={tools.isFetching}
        retry={() => void tools.refetch()}
      />
    );
  }
  const groups = toolGroups(tools.data, chat);
  if (value === null) {
    return (
      <div className="space-y-2">
        <Inherited from={sourceOf(inherited, "tools")}>{choiceSummary(inherited.tools)}</Inherited>
        <Button
          type="button"
          size="xs"
          variant="outline"
          onClick={() => {
            onChange(inherited.tools === null ? everyTool(groups) : [...inherited.tools]);
          }}
        >
          Choose the tools
        </Button>
      </div>
    );
  }
  const toggle = (entry: string, on: boolean) => {
    onChange(withChoice(value, entry, on, groups));
  };
  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-2">
        <p className="text-muted-foreground text-xs">Chosen here: {choiceSummary(value)}.</p>
        <ResetButton
          label="the tools"
          onClick={() => {
            onChange(null);
          }}
        />
      </div>
      <ul className="space-y-1" aria-label="Built-in tools">
        {groups.builtin.map((t) => (
          <ToolCheck
            key={t.name}
            id={`${idPrefix}-${t.name}`}
            tool={t}
            label={t.name}
            checked={chosen(value, t)}
            onChange={(on) => {
              toggle(t.name, on);
            }}
          />
        ))}
      </ul>
      {groups.servers.map(({ server, tools: list }) => {
        const whole = serverEntry(server);
        return (
          <section key={server} className="space-y-1">
            <div className="flex items-center gap-2">
              <Checkbox
                id={`${idPrefix}-${whole}`}
                checked={value.includes(whole)}
                onCheckedChange={(on) => {
                  toggle(whole, on === true);
                }}
              />
              <Label htmlFor={`${idPrefix}-${whole}`} className="text-xs">
                Every tool of <span className="font-mono">{server}</span>, and any it adds
              </Label>
            </div>
            <ul className="space-y-1 pl-5" aria-label={`Tools of ${server}`}>
              {list.map((t) => (
                <ToolCheck
                  key={t.name}
                  id={`${idPrefix}-${t.name}`}
                  tool={t}
                  label={t.name.replace(`mcp__${server}__`, "")}
                  checked={chosen(value, t)}
                  onChange={(on) => {
                    toggle(t.name, on);
                  }}
                />
              ))}
            </ul>
          </section>
        );
      })}
    </div>
  );
}

type ToolCheckProps = {
  id: string;
  tool: Tool;
  label: string;
  checked: boolean;
  onChange: (on: boolean) => void;
};

function ToolCheck({ id, tool, label, checked, onChange }: ToolCheckProps) {
  return (
    <li className="flex items-start gap-2">
      <Checkbox
        id={id}
        checked={checked}
        className="mt-0.5"
        onCheckedChange={(on) => {
          onChange(on === true);
        }}
      />
      <div className="min-w-0">
        <Label htmlFor={id} className="font-mono text-xs break-all">
          {label}
        </Label>
        <p className="text-muted-foreground truncate text-xs">{toolSummary(tool.description)}</p>
      </div>
    </li>
  );
}

type SamplingInputProps = {
  id: string;
  field: SamplingField;
  value: string;
  inherited: string;
  problem: string | undefined;
  onChange: (text: string) => void;
};

function SamplingInput({ id, field, value, inherited, problem, onChange }: SamplingInputProps) {
  const hintId = `${id}-hint`;
  const shared = {
    id,
    value,
    placeholder: inherited,
    "aria-invalid": problem !== undefined,
    "aria-describedby": hintId,
    className: "font-mono text-xs",
  };
  return (
    <div className="space-y-1">
      <div className="flex h-6 items-center justify-between gap-2">
        <Label htmlFor={id}>{field.label}</Label>
        {value !== "" && (
          <ResetButton
            label={field.label.toLowerCase()}
            onClick={() => {
              onChange("");
            }}
          />
        )}
      </div>
      {field.kind === "list" ? (
        <Textarea
          {...shared}
          rows={2}
          onChange={(e) => {
            onChange(e.target.value);
          }}
        />
      ) : (
        <Input
          {...shared}
          inputMode={field.kind === "text" ? "text" : "decimal"}
          autoComplete="off"
          onChange={(e) => {
            onChange(e.target.value);
          }}
        />
      )}
      <p
        id={hintId}
        className={
          problem === undefined ? "text-muted-foreground text-xs" : "text-destructive text-xs"
        }
      >
        {problem ?? field.hint}
      </p>
    </div>
  );
}
