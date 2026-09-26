/**
 * The settings editor's own fields: the model, a setting that is on or off,
 * and the tool choice, each set here or left to fall through.
 */

import type { ConfigLayer, Configuration, Model } from "@/api/types";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { ChoiceGroup, FieldHeader, Inherited, ResetButton } from "@/features/profiles/fields";
import { choiceSummary, everyTool, layerName, sourceOf } from "@/features/profiles/form";
import type { Draft, ToolGroups } from "@/features/profiles/form";
import { ToolPicker } from "@/features/profiles/ToolPicker";
import { formatTokens } from "@/lib/format";

/** inheritSelect is the model select's value for "not set here". */
const inheritSelect = "__inherit";

type ModelFieldProps = {
  id: string;
  draft: Draft;
  inherited: Configuration;
  models: readonly Model[];
  /** runs is the model the layer runs: its own choice, or the one it inherits. */
  runs: Model | undefined;
  onChange: (patch: Partial<Draft>) => void;
};

export function ModelField({ id, draft, inherited, models, runs, onChange }: ModelFieldProps) {
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
export function SwitchField({
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
export function ToolsField({ idPrefix, value, inherited, groups, onChange }: ToolsFieldProps) {
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
