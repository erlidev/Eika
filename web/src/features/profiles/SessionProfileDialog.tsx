/**
 * The session's profile, as the status bar shows it beside the model, and
 * the dialog it opens: which profile the session runs with, and the same
 * editor the settings use, over what the session sets for itself.
 */

import { ExternalLink, SlidersHorizontal } from "lucide-react";
import { useState } from "react";

import type { SessionConfiguration, Profiles } from "@/api/types";
import { ActionError, LoadError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { toDraft, toSettings } from "@/features/profiles/form";
import type { Draft, EditorSection, SamplingProblem } from "@/features/profiles/form";
import {
  useDraftConfiguration,
  useProfiles,
  useSaveSessionSettings,
  useSessionConfiguration,
} from "@/features/profiles/queries";
import { SettingsEditor } from "@/features/profiles/SettingsEditor";
import { useConfigEditor } from "@/features/profiles/store";
import { cn } from "@/lib/utils";

export type SessionProfileProps = {
  sessionId: string;
  /** chat says the session has no workspace, so only a chat's settings apply. */
  chat: boolean;
  /** overridden says the session sets something of its own over its profile. */
  overridden: boolean;
};

/**
 * SessionProfile is the status bar's profile button: the profile's name,
 * marked when the session overrides it, which opens the session's editor.
 */
export function SessionProfile({ sessionId, chat, overridden }: SessionProfileProps) {
  const config = useSessionConfiguration(sessionId);
  const target = useConfigEditor((s) => s.session);
  const editSession = useConfigEditor((s) => s.editSession);
  const closeSession = useConfigEditor((s) => s.closeSession);
  const open = target?.id === sessionId;
  const name = config.data?.resolved.profile_name ?? "Profile";
  return (
    <>
      <button
        type="button"
        aria-label={`Profile: ${name}${overridden ? ", overridden by this session" : ""}`}
        title={
          overridden
            ? `Profile ${name}, with this session's own settings. Click to change them.`
            : `Profile ${name}. Click to change it or override it for this session.`
        }
        className={cn(
          "hover:bg-accent flex h-6 items-center gap-1 rounded-md border px-1.5 transition-colors",
          "focus-visible:ring-ring focus-visible:ring-1 focus-visible:outline-none",
        )}
        onClick={() => {
          editSession(sessionId, "model");
        }}
      >
        <SlidersHorizontal aria-hidden className="size-3" />
        <span className="max-w-32 truncate">{name}</span>
        {overridden && <span aria-hidden className="bg-primary size-1.5 rounded-full" />}
      </button>
      <Dialog
        open={open}
        onOpenChange={(next) => {
          if (!next) closeSession();
        }}
      >
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>This session&apos;s configuration</DialogTitle>
            <DialogDescription>
              The profile the session runs with, and what it sets for itself over it. A change
              applies from the next message.
            </DialogDescription>
          </DialogHeader>
          {open && (
            <SessionSettings
              sessionId={sessionId}
              chat={chat}
              initialSection={target.section}
              onDone={closeSession}
            />
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}

type SessionSettingsProps = {
  sessionId: string;
  chat: boolean;
  initialSection: EditorSection;
  onDone: () => void;
};

function SessionSettings({ sessionId, chat, initialSection, onDone }: SessionSettingsProps) {
  const config = useSessionConfiguration(sessionId);
  const profiles = useProfiles();
  if (config.isPending || profiles.isPending) {
    return <Notice tone="pending">Loading the configuration…</Notice>;
  }
  if (config.isError || profiles.isError) {
    const failed = config.error ?? profiles.error;
    return (
      <LoadError
        what="the configuration"
        error={failed ?? new Error("unknown")}
        retrying={config.isFetching || profiles.isFetching}
        retry={() => {
          void config.refetch();
          void profiles.refetch();
        }}
      />
    );
  }
  return (
    <SessionSettingsForm
      sessionId={sessionId}
      chat={chat}
      config={config.data}
      profiles={profiles.data}
      initialSection={initialSection}
      onDone={onDone}
    />
  );
}

type SessionSettingsFormProps = {
  sessionId: string;
  chat: boolean;
  config: SessionConfiguration;
  profiles: Profiles;
  initialSection: EditorSection;
  onDone: () => void;
};

/** defaultChoice is the profile select's value for "whichever is the default". */
const defaultChoice = "__default";

function SessionSettingsForm({
  sessionId,
  chat,
  config,
  profiles,
  initialSection,
  onDone,
}: SessionSettingsFormProps) {
  const save = useSaveSessionSettings(sessionId);
  const editProfile = useConfigEditor((s) => s.editProfile);
  const [profileId, setProfileId] = useState(config.profile_id ?? "");
  const [draft, setDraft] = useState<Draft>(() => toDraft(config.overrides, config.tools));
  const [problems, setProblems] = useState<SamplingProblem[]>([]);
  const list = profiles.profiles ?? [];
  const savedProfile = config.profile_id ?? "";
  const savedModel = config.overrides.model_id ?? "";
  const changed = profileId !== savedProfile || draft.model_id !== savedModel;
  const drafted = useDraftConfiguration(
    sessionId,
    changed ? { profileId, modelId: draft.model_id } : undefined,
  );
  const inherited = (changed ? drafted.data?.inherited : undefined) ?? config.inherited;
  const fallback = list.find((p) => p.id === profiles.default)?.name ?? "the default";

  const submit = () => {
    const out = toSettings(draft);
    setProblems(out.problems);
    if (!("settings" in out)) return;
    save.mutate({ profileId, overrides: out.settings, tools: draft.tools }, { onSuccess: onDone });
  };

  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
    >
      <div className="space-y-1.5">
        <div className="flex items-center justify-between gap-2">
          <Label htmlFor="session-profile">Profile</Label>
          <Button
            type="button"
            size="xs"
            variant="ghost"
            onClick={() => {
              editProfile(profileId === "" ? profiles.default : profileId, "model");
            }}
          >
            <ExternalLink aria-hidden />
            Edit the profile
          </Button>
        </div>
        <Select
          value={profileId === "" ? defaultChoice : profileId}
          onValueChange={(value) => {
            setProfileId(value === defaultChoice ? "" : value);
          }}
        >
          <SelectTrigger id="session-profile" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={defaultChoice}>
              <span className="text-muted-foreground">The default, now {fallback}</span>
            </SelectItem>
            {list.map((p) => (
              <SelectItem key={p.id} value={p.id}>
                {p.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <SettingsEditor
        idPrefix="session"
        draft={draft}
        onChange={setDraft}
        inherited={inherited}
        prompts={profiles.prompts}
        kind={chat ? "chat" : "workspace"}
        problems={problems}
        initialSection={initialSection}
      />
      {problems.length > 0 && (
        <Notice tone="error">
          Some parameters are not numbers; the tabs marked with a count hold them.
        </Notice>
      )}
      {save.isError && <ActionError action="save the session's configuration" error={save.error} />}
      <div className="flex justify-end gap-2 border-t pt-3">
        <Button type="button" variant="ghost" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? "Saving…" : "Save"}
        </Button>
      </div>
    </form>
  );
}
