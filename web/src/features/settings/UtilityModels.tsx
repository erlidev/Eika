/**
 * The models the harness sends its own small tasks to, one choice per task.
 * A task with no model does not run.
 */

import type { SettingsState } from "@/api/types";
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
import { useModels, useProviders } from "@/features/providers";
import { ModelSelectItems } from "@/features/settings/ModelSelectItems";
import { settingKeys, useSaveSettings, utilityModels } from "@/features/settings/queries";

/** off is the select value for a task with no model, which Radix cannot give as "". */
const off = "__off";

/** utilityTasks are the tasks internal/utility names, as the settings show them. */
const utilityTasks = [
  {
    task: "session_title",
    label: "Session titles",
    hint: "Names a new session after the first message sent to it. Off keeps the title New session.",
  },
] as const;

type UtilityModelsProps = { state: SettingsState };

export function UtilityModels({ state }: UtilityModelsProps) {
  const models = useModels();
  const providers = useProviders();
  const save = useSaveSettings();
  const all = models.data?.models ?? [];
  // A name the harness would refuse, such as a deleted model's, reads as off
  // and is not written back.
  const assigned: Record<string, string> = {};
  for (const { task } of utilityTasks) {
    const name = utilityModels(state)[task] ?? "";
    assigned[task] = all.some((m) => m.name === name) ? name : "";
  }

  return (
    <div className="space-y-3">
      <div>
        <h3 className="text-sm font-medium">Utility models</h3>
        <p className="text-muted-foreground text-xs">
          Models the harness calls for its own small tasks, one request each with thinking off. A
          small, fast model is enough.
        </p>
      </div>
      {utilityTasks.map(({ task, label, hint }) => (
        <div key={task} className="space-y-1.5">
          <Label htmlFor={`utility-${task}`}>{label}</Label>
          <Select
            value={assigned[task] === "" ? off : assigned[task]}
            disabled={all.length === 0 || save.isPending}
            aria-describedby={`utility-${task}-hint`}
            onValueChange={(value) => {
              save.mutate({
                [settingKeys.utilityModels]: { ...assigned, [task]: value === off ? "" : value },
              });
            }}
          >
            <SelectTrigger id={`utility-${task}`} className="w-full">
              <SelectValue placeholder={models.isPending ? "Loading the models…" : "Off"} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={off}>Off</SelectItem>
              <SelectSeparator />
              <ModelSelectItems models={all} providers={providers.data?.providers ?? []} />
            </SelectContent>
          </Select>
          <p id={`utility-${task}-hint`} className="text-muted-foreground text-xs">
            {hint}
          </p>
        </div>
      ))}
      {save.isError && <ActionError action="save the utility models" error={save.error} />}
    </div>
  );
}
