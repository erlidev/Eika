/**
 * The harness-wide choices: the default model, the image new workspaces run,
 * how far subagents may spread, and a way back into the guided setup.
 */

import { useState } from "react";

import type { SettingsState } from "@/api/types";
import { LoadError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
import { DefaultModelSelect } from "@/features/settings/DefaultModelSelect";
import {
  settingKeys,
  settingNumber,
  settingString,
  useSaveSettings,
  useSettings,
} from "@/features/settings/queries";
import { SystemCheck } from "@/features/settings/SystemCheck";

export function GeneralSettings() {
  const settings = useSettings();
  // The saves live here, above the forms they serve, so that a form that
  // remounts on the value it saved still shows how the save went.
  const saveImage = useSaveSettings();
  const saveAgents = useSaveSettings();
  if (settings.isError) {
    return (
      <LoadError
        what="the settings"
        error={settings.error}
        retrying={settings.isFetching}
        retry={() => void settings.refetch()}
      />
    );
  }
  if (!settings.data) return <Notice tone="pending">Loading the settings…</Notice>;
  return (
    <div className="space-y-6">
      <DefaultModelSelect />
      <Separator />
      {/* Keyed by the stored values, so a form starts from what is stored
          now and a save or refetch elsewhere leaves its unsaved edits alone. */}
      <SandboxForm
        key={`image-${storedKey(settings.data, settingKeys.sandboxImage)}`}
        state={settings.data}
        save={saveImage}
      />
      <SystemCheck />
      <Separator />
      <SubagentForm
        key={`agents-${storedKey(settings.data, settingKeys.subagentMaxDepth)}-${storedKey(settings.data, settingKeys.subagentMaxChildren)}`}
        state={settings.data}
        save={saveAgents}
      />
      <Separator />
      <SetupAgain />
    </div>
  );
}

type SaveSettings = ReturnType<typeof useSaveSettings>;

/** storedKey renders a stored setting for a form's React key. */
function storedKey(state: SettingsState, key: string): string {
  return JSON.stringify(state.settings[key] ?? null);
}

function SandboxForm({ state, save }: { state: SettingsState; save: SaveSettings }) {
  const stored = settingString(state, settingKeys.sandboxImage);
  const [image, setImage] = useState(stored);
  const changed = image.trim() !== stored;
  const invalid = /\s/.test(image.trim());
  const tooLong = image.trim().length > 255;

  return (
    <form
      className="space-y-1.5"
      onSubmit={(e) => {
        e.preventDefault();
        if (!changed || invalid || tooLong) return;
        save.mutate({ [settingKeys.sandboxImage]: image.trim() === "" ? null : image.trim() });
      }}
    >
      <Label htmlFor="sandbox-image">Sandbox image</Label>
      <div className="flex gap-2">
        <Input
          id="sandbox-image"
          value={image}
          className="font-mono"
          placeholder={state.defaults.sandbox_image}
          maxLength={255}
          aria-invalid={invalid}
          aria-describedby={invalid ? "sandbox-image-problem" : undefined}
          onChange={(e) => {
            setImage(e.target.value);
          }}
        />
        <Button
          type="submit"
          variant="outline"
          disabled={!changed || invalid || tooLong || save.isPending}
        >
          Save
        </Button>
      </div>
      {invalid && (
        <p id="sandbox-image-problem" className="text-destructive text-xs">
          An image reference has no spaces. Remove them, as in eika-sandbox:latest.
        </p>
      )}
      <p className="text-muted-foreground text-xs">
        The image a new workspace runs when it names none. Leave empty for{" "}
        <code>{state.defaults.sandbox_image}</code>, which the compose stack builds. Any image
        works: the harness copies its daemon in.
      </p>
      {save.isError && <Notice tone="error">{save.error.message}</Notice>}
    </form>
  );
}

function SubagentForm({ state, save }: { state: SettingsState; save: SaveSettings }) {
  const [depth, setDepth] = useState(
    settingNumber(state, settingKeys.subagentMaxDepth) ?? state.defaults.subagent_max_depth,
  );
  const [children, setChildren] = useState(
    settingNumber(state, settingKeys.subagentMaxChildren) ?? state.defaults.subagent_max_children,
  );
  const depthProblem =
    Number.isInteger(depth) && depth >= 1 && depth <= 8
      ? null
      : "Enter a whole number of levels from 1 to 8.";
  const childrenProblem =
    Number.isInteger(children) && children >= 1 && children <= 16
      ? null
      : "Enter a whole number of children from 1 to 16.";
  const invalid = depthProblem !== null || childrenProblem !== null;

  return (
    <form
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (invalid) return;
        save.mutate({
          [settingKeys.subagentMaxDepth]: depth,
          [settingKeys.subagentMaxChildren]: children,
        });
      }}
    >
      <div>
        <h3 className="text-sm font-medium">Subagents</h3>
        <p className="text-muted-foreground text-xs">
          How far the tree of child agents may grow. Each child works in a sandbox of its own.
        </p>
      </div>
      <div className="grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
        <div className="space-y-1">
          <Label htmlFor="subagent-depth" className="text-xs">
            Levels below a session (1–8)
          </Label>
          <Input
            id="subagent-depth"
            type="number"
            min={1}
            max={8}
            step={1}
            aria-invalid={depthProblem !== null}
            aria-describedby={depthProblem ? "subagent-problem" : undefined}
            value={Number.isFinite(depth) ? depth : ""}
            onChange={(e) => {
              setDepth(Number(e.target.value === "" ? Number.NaN : e.target.value));
            }}
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="subagent-children" className="text-xs">
            Children at a time (1–16)
          </Label>
          <Input
            id="subagent-children"
            type="number"
            min={1}
            max={16}
            step={1}
            aria-invalid={childrenProblem !== null}
            aria-describedby={childrenProblem ? "subagent-problem" : undefined}
            value={Number.isFinite(children) ? children : ""}
            onChange={(e) => {
              setChildren(Number(e.target.value === "" ? Number.NaN : e.target.value));
            }}
          />
        </div>
        <Button type="submit" variant="outline" disabled={invalid || save.isPending}>
          Save
        </Button>
      </div>
      {invalid && (
        <p id="subagent-problem" className="text-destructive text-xs">
          {depthProblem ?? childrenProblem}
        </p>
      )}
      {save.isSuccess && <Notice tone="success">Saved. The next spawn uses these limits.</Notice>}
      {save.isError && <Notice tone="error">{save.error.message}</Notice>}
    </form>
  );
}

function SetupAgain() {
  const save = useSaveSettings();
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h3 className="text-sm font-medium">Guided setup</h3>
          <p className="text-muted-foreground text-xs">
            Walk through connecting a provider, checking the sandbox, and adding a project again.
          </p>
        </div>
        <Button
          variant="outline"
          size="sm"
          disabled={save.isPending}
          onClick={() => {
            save.mutate({ [settingKeys.setupComplete]: false });
          }}
        >
          Run the setup again
        </Button>
      </div>
      {save.isError && <Notice tone="error">{save.error.message}</Notice>}
    </div>
  );
}
