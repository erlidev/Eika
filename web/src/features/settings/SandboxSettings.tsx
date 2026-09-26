/**
 * The Sandbox tab: the limits and network a new workspace gets unless it is
 * created with its own. A workspace keeps what it was created with; its
 * Sandbox panel changes that one workspace.
 */

import { useState } from "react";

import type { SandboxHost } from "@/api/types";
import { ActionError, LoadError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { checkDraft, draftOf, EgressFields, LimitsFields } from "@/features/sandbox";
import type { SandboxDraft } from "@/features/sandbox";
import {
  sandboxDefaults,
  settingKeys,
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
