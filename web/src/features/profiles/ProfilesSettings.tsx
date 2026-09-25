/**
 * The Profiles tab of the settings: every profile, the default one, and an
 * editor for each. A profile is a named configuration of what a run sends;
 * a session runs with the default one until it picks another.
 */

import { ChevronLeft, ChevronRight, Plus, Trash2 } from "lucide-react";
import { useState } from "react";

import type { Profile, Profiles } from "@/api/types";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { ActionError, LoadError, Notice } from "@/components/Notice";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { choiceSummary, toDraft, toSettings } from "@/features/profiles/form";
import type { Draft, SamplingProblem } from "@/features/profiles/form";
import {
  useCreateProfile,
  useDeleteProfile,
  useInheritedProfile,
  useProfiles,
  useUpdateProfile,
} from "@/features/profiles/queries";
import { SettingsEditor } from "@/features/profiles/SettingsEditor";
import { settingKeys, useSaveSettings } from "@/features/settings/queries";

/** newProfile is the editor's selection for a profile not saved yet. */
const newProfile = "__new";

export function ProfilesSettings() {
  const profiles = useProfiles();
  const [selected, setSelected] = useState("");

  if (profiles.isPending) return <Notice tone="pending">Loading the profiles…</Notice>;
  if (profiles.isError) {
    return (
      <LoadError
        what="the profiles"
        error={profiles.error}
        retrying={profiles.isFetching}
        retry={() => void profiles.refetch()}
      />
    );
  }
  const list = profiles.data.profiles ?? [];
  const open = list.find((p) => p.id === selected);
  const back = () => {
    setSelected("");
  };
  if (selected === newProfile) {
    return (
      <ProfileView key={newProfile} data={profiles.data} onBack={back} onSaved={setSelected} />
    );
  }
  if (open !== undefined) {
    return (
      <ProfileView
        key={open.id}
        data={profiles.data}
        profile={open}
        onBack={back}
        onSaved={setSelected}
      />
    );
  }
  return (
    <ProfileList
      data={profiles.data}
      onOpen={setSelected}
      onAdd={() => {
        setSelected(newProfile);
      }}
    />
  );
}

type ProfileListProps = {
  data: Profiles;
  onOpen: (id: string) => void;
  onAdd: () => void;
};

function ProfileList({ data, onOpen, onAdd }: ProfileListProps) {
  const list = data.profiles ?? [];
  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-4">
        <p className="text-muted-foreground text-sm">
          A profile says what a run sends: the model, the base prompt, extra instructions, the
          tools, and the sampling parameters. What a profile leaves unset comes from the model and
          the defaults; a session can override its profile from the status bar.
        </p>
        <Button size="sm" onClick={onAdd}>
          <Plus aria-hidden />
          New profile
        </Button>
      </div>
      <DefaultProfileSelect data={data} />
      <ul className="divide-y overflow-hidden rounded-md border" aria-label="Profiles">
        {list.map((profile) => (
          <li key={profile.id}>
            <button
              type="button"
              onClick={() => {
                onOpen(profile.id);
              }}
              className="hover:bg-accent focus-visible:ring-ring flex w-full items-center gap-3 px-3 py-2.5 text-left transition-colors focus-visible:ring-1 focus-visible:outline-none focus-visible:ring-inset"
            >
              <div className="min-w-40 flex-1">
                <p className="flex min-w-0 items-center gap-2">
                  <span className="truncate text-sm font-medium">{profile.name}</span>
                  {profile.id === data.default && (
                    <Badge variant="secondary" className="h-4 shrink-0 px-1 text-2xs">
                      default
                    </Badge>
                  )}
                </p>
                <p className="text-muted-foreground truncate text-xs">{profileSummary(profile)}</p>
              </div>
              <ChevronRight aria-hidden className="text-muted-foreground size-4 shrink-0" />
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** profileSummary is a profile's row detail: its description, or what it sets. */
function profileSummary(profile: Profile): string {
  if (profile.description !== "") return profile.description;
  const parts: string[] = [];
  if (profile.model_id !== undefined) parts.push(profile.inherited.model);
  if (profile.workspace_prompt !== null || profile.chat_prompt !== null) parts.push("own prompt");
  if (profile.instructions !== null) parts.push("instructions");
  if (profile.tools !== null) parts.push(choiceSummary(profile.tools));
  const sampling = Object.keys(profile.sampling).length;
  if (sampling > 0)
    parts.push(`${String(sampling)} sampling parameter${sampling === 1 ? "" : "s"}`);
  return parts.length === 0
    ? "Sets nothing: runs as the model and the defaults say"
    : parts.join(" · ");
}

function DefaultProfileSelect({ data }: { data: Profiles }) {
  const save = useSaveSettings();
  return (
    <div className="space-y-1.5">
      <Label htmlFor="default-profile">Default profile</Label>
      <Select
        value={data.default}
        onValueChange={(value) => {
          save.mutate({ [settingKeys.defaultProfile]: value });
        }}
      >
        <SelectTrigger id="default-profile" className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {(data.profiles ?? []).map((p) => (
            <SelectItem key={p.id} value={p.id}>
              {p.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <p className="text-muted-foreground text-xs">
        What a session runs with until it picks a profile of its own.
      </p>
      {save.isError && <ActionError action="change the default profile" error={save.error} />}
    </div>
  );
}

type ProfileViewProps = {
  data: Profiles;
  /** profile is the one edited; absent, the view creates one. */
  profile?: Profile;
  onBack: () => void;
  /** onSaved receives the id of the profile saved, which the view shows next. */
  onSaved: (id: string) => void;
};

/** emptyDraft is a profile that sets nothing. */
function emptyDraft(): Draft {
  return toDraft(
    {
      workspace_prompt: null,
      chat_prompt: null,
      instructions: null,
      context_files: null,
      sampling: {},
    },
    null,
  );
}

function ProfileView({ data, profile, onBack, onSaved }: ProfileViewProps) {
  const create = useCreateProfile();
  const update = useUpdateProfile();
  const remove = useDeleteProfile();
  const [name, setName] = useState(profile?.name ?? "");
  const [description, setDescription] = useState(profile?.description ?? "");
  const [draft, setDraft] = useState<Draft>(() =>
    profile === undefined ? emptyDraft() : toDraft(profile, profile.tools),
  );
  const [problems, setProblems] = useState<SamplingProblem[]>([]);
  const [confirming, setConfirming] = useState(false);
  const saving = create.isPending || update.isPending;
  const saveError = create.error ?? update.error;
  // A saved profile carries what it falls through to; a new one, or one
  // whose model changed, asks with the model chosen, which is all that
  // falls through depends on.
  const changed = profile === undefined || draft.model_id !== (profile.model_id ?? "");
  const drafted = useInheritedProfile(draft.model_id, changed);
  const inherited = (changed ? drafted.data : undefined) ?? profile?.inherited;
  const last = (data.profiles ?? []).length <= 1;

  const save = () => {
    const out = toSettings(draft);
    setProblems(out.problems);
    if (!("settings" in out)) return;
    const input = {
      ...out.settings,
      name: name.trim(),
      description: description.trim(),
      tools: draft.tools,
    };
    if (profile === undefined) {
      create.mutate(input, {
        onSuccess: (created) => {
          onSaved(created.id);
        },
      });
    } else {
      update.mutate({ id: profile.id, input });
    }
  };

  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        save();
      }}
    >
      <Button type="button" size="xs" variant="ghost" className="-ml-2" onClick={onBack}>
        <ChevronLeft aria-hidden />
        All profiles
      </Button>
      <div className="grid gap-3 sm:grid-cols-[1fr_2fr]">
        <div className="space-y-1.5">
          <Label htmlFor="profile-name">Name</Label>
          <Input
            id="profile-name"
            value={name}
            required
            autoComplete="off"
            onChange={(e) => {
              setName(e.target.value);
            }}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="profile-description">Description</Label>
          <Input
            id="profile-description"
            value={description}
            autoComplete="off"
            placeholder="What the profile is for"
            onChange={(e) => {
              setDescription(e.target.value);
            }}
          />
        </div>
      </div>
      {inherited !== undefined && (
        <SettingsEditor
          idPrefix="profile"
          draft={draft}
          onChange={setDraft}
          inherited={inherited}
          prompts={data.prompts}
          kind="profile"
          problems={problems}
        />
      )}
      {problems.length > 0 && (
        <Notice tone="error">
          Some sampling parameters are not numbers; see the Sampling tab.
        </Notice>
      )}
      {saveError && (
        <ActionError
          action={profile === undefined ? "add the profile" : `save ${profile.name}`}
          error={saveError}
        />
      )}
      {update.isSuccess && !saving && <Notice tone="success">Saved. The next run uses it.</Notice>}
      <div className="flex flex-wrap items-center justify-end gap-2 border-t pt-3">
        {profile !== undefined && (
          <Button
            type="button"
            variant="outline"
            className="text-destructive mr-auto"
            disabled={last || remove.isPending}
            title={last ? "The last profile cannot be deleted" : undefined}
            onClick={() => {
              setConfirming(true);
            }}
          >
            <Trash2 aria-hidden />
            Delete
          </Button>
        )}
        <Button type="button" variant="ghost" onClick={onBack}>
          Cancel
        </Button>
        <Button type="submit" disabled={saving || name.trim() === ""}>
          {saving ? "Saving…" : profile === undefined ? "Add profile" : "Save profile"}
        </Button>
      </div>
      {remove.isError && <ActionError action="delete the profile" error={remove.error} />}
      {profile !== undefined && (
        <ConfirmDialog
          open={confirming}
          onOpenChange={setConfirming}
          title={`Delete ${profile.name}?`}
          description="The sessions that chose it run with the default profile from their next message. Their own overrides stay."
          confirmLabel="Delete"
          onConfirm={() => {
            remove.mutate(profile.id, { onSuccess: onBack });
          }}
        />
      )}
    </form>
  );
}
