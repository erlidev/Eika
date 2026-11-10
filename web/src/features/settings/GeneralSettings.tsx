/**
 * The harness-wide choices: the default model, the model classes, the image
 * the home computer is created from, and a way back into the guided setup.
 */

import { useState } from "react";

import type { SettingsState } from "@/api/types";
import { ActionError, LoadError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
import { DefaultModelSelect } from "@/features/settings/DefaultModelSelect";
import { ModelClasses } from "@/features/settings/ModelClasses";
import {
  settingKeys,
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
      <ModelClasses state={settings.data} />
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
      <SetupAgain />
    </div>
  );
}

type SaveSettings = ReturnType<typeof useSaveSettings>;

/** storedKey renders a stored setting for a form's React key. */
function storedKey(state: SettingsState, key: string): string {
  return JSON.stringify(state.settings[key] ?? null);
}

type SandboxFormProps = { state: SettingsState; save: SaveSettings };

function SandboxForm({ state, save }: SandboxFormProps) {
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
        The image the home computer is created from. Leave empty for{" "}
        <code>{state.defaults.sandbox_image}</code>, which the compose stack builds. A container
        that exists keeps its image, so a change reaches the home computer only when its container
        is made again. Any image works: the harness copies its daemon in.
      </p>
      {save.isError && <ActionError action="save the sandbox image" error={save.error} />}
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
            Walk through connecting a provider and checking the sandbox again.
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
      {save.isError && <ActionError action="reopen the setup" error={save.error} />}
    </div>
  );
}
