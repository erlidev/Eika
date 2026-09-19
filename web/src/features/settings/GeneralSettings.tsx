/**
 * The harness-wide choices: the default model, the image new workspaces run,
 * how far subagents may spread, and a way back into the guided setup.
 */

import { useState } from "react";

import type { SettingsState } from "@/api/types";
import { Notice } from "@/components/Notice";
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
  if (settings.isError) return <Notice tone="error">{settings.error.message}</Notice>;
  if (!settings.data) return <Notice tone="pending">Loading the settings…</Notice>;
  return (
    <div className="space-y-6">
      <DefaultModelSelect />
      <Separator />
      {/* Keyed by the fetch, so a form starts from what is stored now. */}
      <SandboxForm key={`image-${String(settings.dataUpdatedAt)}`} state={settings.data} />
      <SystemCheck />
      <Separator />
      <SubagentForm key={`agents-${String(settings.dataUpdatedAt)}`} state={settings.data} />
      <Separator />
      <SetupAgain />
    </div>
  );
}

function SandboxForm({ state }: { state: SettingsState }) {
  const save = useSaveSettings();
  const stored = settingString(state, settingKeys.sandboxImage);
  const [image, setImage] = useState(stored);
  const changed = image.trim() !== stored;
  const invalid = /\s/.test(image.trim());

  return (
    <form
      className="space-y-1.5"
      onSubmit={(e) => {
        e.preventDefault();
        if (!changed || invalid) return;
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
          onChange={(e) => {
            setImage(e.target.value);
          }}
        />
        <Button type="submit" variant="outline" disabled={!changed || invalid || save.isPending}>
          Save
        </Button>
      </div>
      <p className="text-muted-foreground text-xs">
        The image a new workspace runs when it names none. Leave empty for{" "}
        <code>{state.defaults.sandbox_image}</code>, which the compose stack builds. Any image
        works: the harness copies its daemon in.
      </p>
      {save.isError && <Notice tone="error">{save.error.message}</Notice>}
    </form>
  );
}

function SubagentForm({ state }: { state: SettingsState }) {
  const save = useSaveSettings();
  const [depth, setDepth] = useState(
    settingNumber(state, settingKeys.subagentMaxDepth) ?? state.defaults.subagent_max_depth,
  );
  const [children, setChildren] = useState(
    settingNumber(state, settingKeys.subagentMaxChildren) ?? state.defaults.subagent_max_children,
  );
  const invalid = !(depth >= 1 && depth <= 8) || !(children >= 1 && children <= 16);

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
            value={Number.isFinite(depth) ? depth : ""}
            onChange={(e) => {
              setDepth(Number.parseInt(e.target.value, 10));
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
            value={Number.isFinite(children) ? children : ""}
            onChange={(e) => {
              setChildren(Number.parseInt(e.target.value, 10));
            }}
          />
        </div>
        <Button type="submit" variant="outline" disabled={invalid || save.isPending}>
          Save
        </Button>
      </div>
      {save.isSuccess && <Notice tone="success">Saved. The next spawn uses these limits.</Notice>}
      {save.isError && <Notice tone="error">{save.error.message}</Notice>}
    </form>
  );
}

function SetupAgain() {
  const save = useSaveSettings();
  return (
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
  );
}
