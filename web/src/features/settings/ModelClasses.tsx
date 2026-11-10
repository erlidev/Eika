/**
 * The models behind the model classes. Harness tasks and agents ask for a
 * class, never a model; this maps each class to a model. A class with no model
 * falls back, and the classes served from one pool are named, since they
 * contend for the same slots.
 */

import type { Model, ModelClass, ModelClassName, Provider, SettingsState } from "@/api/types";
import { modelClassNames } from "@/api/types";
import { ActionError } from "@/components/Notice";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { chatModels, modelsOfKind, useModels, useProviders } from "@/features/providers";
import { classInfo, joinNames, sharedWith, titlesStatus } from "@/features/settings/classes";
import { ModelSelectItems } from "@/features/settings/ModelSelectItems";
import { modelClassAssignments, settingKeys, useSaveSettings } from "@/features/settings/queries";

/** unset is the select value for a class with no model, which Radix cannot give as "". */
const unset = "__unset";

type ModelClassesProps = { state: SettingsState };

export function ModelClasses({ state }: ModelClassesProps) {
  const models = useModels();
  const providers = useProviders();
  const save = useSaveSettings();
  const stored = modelClassAssignments(state);

  // What each class may be given: a decision model for the decision class and
  // a chat model for the rest. A stored name outside that, such as a deleted
  // model's, reads as not set and is not written back.
  const offered = (name: ModelClassName): Model[] =>
    name === "decision"
      ? modelsOfKind(models.data?.models, "decision")
      : chatModels(models.data?.models);
  const assigned = (name: ModelClassName): string => {
    const model = stored[name] ?? "";
    return offered(name).some((m) => m.name === model) ? model : "";
  };

  return (
    <div className="space-y-3">
      <div>
        <h3 className="text-sm font-medium">Model classes</h3>
        <p className="text-muted-foreground text-xs">
          Tasks and agents ask for a class, not a model. Classes served from one pool of hardware
          contend for the same slots.
        </p>
      </div>
      {modelClassNames.map((name) => (
        <ClassRow
          key={name}
          name={name}
          resolved={state.classes.find((c) => c.class === name)}
          classes={state.classes}
          titlesModel={state.titles_model}
          assigned={assigned(name)}
          offered={offered(name)}
          providers={providers.data?.providers ?? []}
          loading={models.isPending}
          disabled={save.isPending}
          onChange={(model) => {
            const next: Record<string, string> = {};
            for (const other of modelClassNames) {
              const kept = other === name ? model : assigned(other);
              if (kept !== "") next[other] = kept;
            }
            save.mutate({ [settingKeys.modelClasses]: next });
          }}
        />
      ))}
      {save.isError && <ActionError action="save the model classes" error={save.error} />}
    </div>
  );
}

type ClassRowProps = {
  name: ModelClassName;
  resolved: ModelClass | undefined;
  classes: readonly ModelClass[];
  titlesModel: string;
  assigned: string;
  offered: readonly Model[];
  providers: readonly Provider[];
  loading: boolean;
  disabled: boolean;
  onChange: (model: string) => void;
};

function ClassRow({
  name,
  resolved,
  classes,
  titlesModel,
  assigned,
  offered,
  providers,
  loading,
  disabled,
  onChange,
}: ClassRowProps) {
  const { label, hint } = classInfo[name];
  const shared = resolved === undefined ? [] : sharedWith(classes, resolved);
  const none = offered.length === 0;
  const id = `class-${name}`;
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Select
        value={assigned === "" ? unset : assigned}
        disabled={none || disabled}
        onValueChange={(value) => {
          onChange(value === unset ? "" : value);
        }}
      >
        <SelectTrigger id={id} className="w-full" aria-describedby={`${id}-hint`}>
          <SelectValue placeholder={loading ? "Loading the models…" : "Not set"} />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={unset}>Not set</SelectItem>
          <SelectSeparator />
          <ModelSelectItems models={offered} providers={providers} />
        </SelectContent>
      </Select>
      <div id={`${id}-hint`} className="text-muted-foreground space-y-0.5 text-xs">
        <p>{hint}</p>
        {!loading && none && (
          <p>
            {name === "decision"
              ? "Add a model and mark it a decision model under Models to choose one."
              : "Add a model under Models to choose one."}
          </p>
        )}
        {assigned === "" && resolved !== undefined && resolved.model !== "" && (
          <p>
            Uses <span className="font-medium">{resolved.model}</span>, the {resolved.via ?? "main"}{" "}
            model.
          </p>
        )}
        {name === "fast" && <p>{titlesStatus(classes, titlesModel)}</p>}
        {shared.length > 0 && (
          <p>
            Shares the <span className="font-mono">{resolved?.pool}</span> pool with{" "}
            {joinNames(shared)}.
          </p>
        )}
      </div>
    </div>
  );
}
