/**
 * The Sandbox tab: the limits and network a new workspace gets unless it is
 * created with its own, and how long any workspace keeps running unused. A
 * workspace keeps what it was created with; its Sandbox panel changes that
 * one workspace.
 */

import { useState } from "react";

import type { SandboxHost, SettingsState } from "@/api/types";
import { ActionError, LoadError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
import { checkDraft, draftOf, EgressFields, LimitsFields } from "@/features/sandbox";
import type { SandboxDraft } from "@/features/sandbox";
import {
  sandboxDefaults,
  settingKeys,
  settingNumber,
  useSaveSettings,
  useSettings,
  useSystem,
} from "@/features/settings/queries";
import type { SandboxDefaults } from "@/features/settings/queries";

export function SandboxSettings() {
  const settings = useSettings();
  const system = useSystem();
  // The save lives above the form it serves, so a form that remounts on the
  // value it saved still shows how the save went.
  const save = useSaveSettings();
  const saveIdle = useSaveSettings();
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
  const stored = sandboxDefaults(settings.data);
  return (
    <div className="space-y-4">
      <div>
        <h3 className="text-sm font-medium">New workspaces</h3>
        <p className="text-muted-foreground text-xs">
          The limits and network a workspace is created with unless it names its own. Forks and
          child agents take their parent&apos;s instead. Change one workspace in its Sandbox panel.
        </p>
      </div>
      <DefaultsForm
        key={JSON.stringify(stored)}
        stored={stored}
        host={system.data?.sandbox}
        save={save}
      />
      <Separator />
      <IdleForm
        key={JSON.stringify(settings.data.settings[settingKeys.workspaceIdleMinutes] ?? null)}
        state={settings.data}
        save={saveIdle}
      />
    </div>
  );
}

type DefaultsFormProps = {
  stored: SandboxDefaults;
  host: SandboxHost | undefined;
  save: ReturnType<typeof useSaveSettings>;
};

function DefaultsForm({ stored, host, save }: DefaultsFormProps) {
  const [draft, setDraft] = useState<SandboxDraft>(() => draftOf(stored));
  const { sandbox, problems } = checkDraft(draft, host);
  const change = (next: Partial<SandboxDraft>) => {
    setDraft((d) => ({ ...d, ...next }));
  };
  const invalid = sandbox === undefined;
  return (
    <form
      className="space-y-4"
      aria-label="New workspaces"
      onSubmit={(e) => {
        e.preventDefault();
        if (sandbox === undefined) return;
        save.mutate({
          [settingKeys.sandboxLimits]: sandbox.limits,
          [settingKeys.sandboxEgress]: sandbox.egress,
        });
      }}
    >
      <section aria-label="Limits" className="space-y-2">
        <h4 className="text-sm font-medium">Limits</h4>
        <LimitsFields
          id="sandbox-defaults"
          draft={draft}
          onChange={change}
          host={host}
          problems={problems}
        />
      </section>
      <Separator />
      <EgressFields
        id="sandbox-defaults"
        draft={draft}
        onChange={change}
        host={host}
        problems={problems}
      />
      <div className="flex flex-wrap items-center gap-2">
        <Button type="submit" variant="outline" disabled={invalid || save.isPending}>
          Save
        </Button>
        {invalid && (
          <p className="text-destructive text-xs">Fix the fields marked above to save.</p>
        )}
      </div>
      {save.isSuccess && (
        <Notice tone="success">Saved. The next workspace is created with these.</Notice>
      )}
      {save.isError && <ActionError action="save the sandbox defaults" error={save.error} />}
    </form>
  );
}

/** maxIdleMinutes is the longest idle timeout the harness accepts: a week. */
const maxIdleMinutes = 7 * 24 * 60;

type IdleFormProps = { state: SettingsState; save: ReturnType<typeof useSaveSettings> };

function IdleForm({ state, save }: IdleFormProps) {
  const [minutes, setMinutes] = useState(
    settingNumber(state, settingKeys.workspaceIdleMinutes) ?? state.defaults.workspace_idle_minutes,
  );
  const problem =
    Number.isInteger(minutes) && minutes >= 0 && minutes <= maxIdleMinutes
      ? null
      : `Enter a whole number of minutes from 0 to ${String(maxIdleMinutes)}.`;

  return (
    <form
      className="space-y-1.5"
      aria-label="Idle workspaces"
      onSubmit={(e) => {
        e.preventDefault();
        if (problem !== null) return;
        save.mutate({ [settingKeys.workspaceIdleMinutes]: minutes });
      }}
    >
      <h3 className="text-sm font-medium">Idle workspaces</h3>
      <Label htmlFor="idle-minutes" className="text-xs">
        Stop after this many minutes without a run
      </Label>
      <div className="flex gap-2">
        <Input
          id="idle-minutes"
          type="number"
          min={0}
          max={maxIdleMinutes}
          step={1}
          className="max-w-32"
          aria-invalid={problem !== null}
          aria-describedby={problem ? "idle-minutes-problem" : "idle-minutes-hint"}
          value={Number.isFinite(minutes) ? minutes : ""}
          onChange={(e) => {
            setMinutes(Number(e.target.value === "" ? Number.NaN : e.target.value));
          }}
        />
        <Button type="submit" variant="outline" disabled={problem !== null || save.isPending}>
          Save
        </Button>
      </div>
      {problem && (
        <p id="idle-minutes-problem" className="text-destructive text-xs">
          {problem}
        </p>
      )}
      <p id="idle-minutes-hint" className="text-muted-foreground text-xs">
        A running workspace stops once no agent run has used it for this long; its files stay. 0
        keeps workspaces running until you stop them. The default is{" "}
        {state.defaults.workspace_idle_minutes}.
      </p>
      {save.isSuccess && <Notice tone="success">Saved.</Notice>}
      {save.isError && <ActionError action="save the idle timeout" error={save.error} />}
    </form>
  );
}
