/**
 * The Compaction tab: when a conversation that outgrows its model's context
 * window is summarized, how much of it stays verbatim, and the prompts that
 * ask for the summary. A session compacts on request with the Compact button
 * under the message box, or with /compact and what to focus on.
 */

import { useState } from "react";

import type { CompactionPrompts, CompactionSettings as Setting } from "@/api/types";
import { ActionError, LoadError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import {
  compactionSetting,
  promptNames,
  promptProblem,
  savedCompaction,
  settingCompaction,
  tokensProblem,
} from "@/features/settings/compaction";
import { useSaveSettings, useSettings } from "@/features/settings/queries";

/** promptLabels name each prompt and say when it is sent. */
const promptLabels: Record<keyof CompactionPrompts, { label: string; help: string }> = {
  summary: {
    label: "Summary prompt",
    help: "Asks for the first summary of a conversation.",
  },
  update: {
    label: "Update prompt",
    help: "Asks for a new summary that folds the newer messages into the one the conversation already begins with.",
  },
  turn_prefix: {
    label: "Split turn prompt",
    help: "Asks for a summary of the start of a turn too large to keep whole, when the cut falls inside it.",
  },
};

export function CompactionSettings() {
  const settings = useSettings();
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
  const stored = compactionSetting(settings.data);
  return (
    <div className="space-y-4">
      <div>
        <h3 className="text-sm font-medium">Compaction</h3>
        <p className="text-muted-foreground text-xs">
          When a conversation outgrows its model&apos;s context window, its older part is summarized
          by the same model and the summary takes its place; the newest messages stay as they are.
          The whole conversation stays in the session. Compact a session yourself with the Compact
          button under the message box, or type /compact and what the summary should focus on.
        </p>
      </div>
      <CompactionForm
        key={JSON.stringify(stored)}
        stored={stored}
        builtIn={settings.data.defaults.compaction.prompts}
        save={save}
      />
    </div>
  );
}

type CompactionFormProps = {
  stored: Setting;
  builtIn: CompactionPrompts;
  save: ReturnType<typeof useSaveSettings>;
};

function CompactionForm({ stored, builtIn, save }: CompactionFormProps) {
  const [draft, setDraft] = useState<Setting>(stored);
  const reserveProblem = tokensProblem(draft.reserve_tokens);
  const keepProblem = tokensProblem(draft.keep_recent_tokens);
  const promptProblems = promptNames.map((name) => promptProblem(draft.prompts[name]));
  const invalid =
    reserveProblem !== undefined ||
    keepProblem !== undefined ||
    promptProblems.some((p) => p !== undefined);
  const setPrompt = (name: keyof CompactionPrompts, text: string) => {
    setDraft((d) => ({ ...d, prompts: { ...d.prompts, [name]: text } }));
  };

  return (
    <form
      className="space-y-4"
      aria-label="Compaction"
      onSubmit={(e) => {
        e.preventDefault();
        if (invalid) return;
        save.mutate({ [settingCompaction]: savedCompaction(draft) });
      }}
    >
      <div className="flex items-start justify-between gap-4 rounded-md border p-3">
        <div className="space-y-0.5">
          <Label htmlFor="compaction-auto" className="text-sm">
            Compact automatically
          </Label>
          <p className="text-muted-foreground text-xs">
            Before a request that would leave less of the window free than the reserve, and once
            after an endpoint refuses a request as too large, which is then sent again. Off, a
            conversation that outgrows the window fails its run until it is compacted by hand.
          </p>
        </div>
        <Switch
          id="compaction-auto"
          checked={draft.auto}
          onCheckedChange={(auto) => {
            setDraft((d) => ({ ...d, auto }));
          }}
        />
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <TokensField
          id="compaction-reserve"
          label="Reserve (tokens)"
          value={draft.reserve_tokens}
          problem={reserveProblem}
          help="How much of the window stays free for the reply. The summary is asked for with 80% of it as its budget."
          onChange={(reserve_tokens) => {
            setDraft((d) => ({ ...d, reserve_tokens }));
          }}
        />
        <TokensField
          id="compaction-keep"
          label="Keep recent (tokens)"
          value={draft.keep_recent_tokens}
          problem={keepProblem}
          help="Roughly how much of the newest conversation stays verbatim after a compaction."
          onChange={(keep_recent_tokens) => {
            setDraft((d) => ({ ...d, keep_recent_tokens }));
          }}
        />
      </div>
      <p className="text-muted-foreground text-xs">
        A model with a small window uses at most a quarter of it for either, so that what a
        compaction leaves fits well below the point where it compacts again.
      </p>

      <Separator />

      {promptNames.map((name, index) => (
        <PromptField
          key={name}
          name={name}
          value={draft.prompts[name]}
          builtIn={builtIn[name]}
          problem={promptProblems[index]}
          onChange={(text) => {
            setPrompt(name, text);
          }}
        />
      ))}

      <div className="flex flex-wrap items-center gap-2">
        <Button type="submit" variant="outline" disabled={invalid || save.isPending}>
          Save
        </Button>
        <Button
          type="button"
          variant="ghost"
          disabled={save.isPending}
          onClick={() => {
            save.mutate({ [settingCompaction]: null });
          }}
        >
          Restore the defaults
        </Button>
        {invalid && (
          <p className="text-destructive text-xs">Fix the fields marked above to save.</p>
        )}
      </div>
      {save.isSuccess && <Notice tone="success">Saved. The next run compacts with these.</Notice>}
      {save.isError && <ActionError action="save the compaction settings" error={save.error} />}
    </form>
  );
}

type TokensFieldProps = {
  id: string;
  label: string;
  value: number;
  problem: string | undefined;
  help: string;
  onChange: (value: number) => void;
};

function TokensField({ id, label, value, problem, help, onChange }: TokensFieldProps) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        type="number"
        inputMode="numeric"
        value={Number.isNaN(value) ? "" : value}
        aria-invalid={problem !== undefined}
        aria-describedby={problem === undefined ? `${id}-help` : `${id}-problem`}
        onChange={(e) => {
          onChange(e.target.value === "" ? Number.NaN : Number(e.target.value));
        }}
      />
      {problem !== undefined && (
        <p id={`${id}-problem`} className="text-destructive text-xs">
          {problem}
        </p>
      )}
      <p id={`${id}-help`} className="text-muted-foreground text-xs">
        {help}
      </p>
    </div>
  );
}

type PromptFieldProps = {
  name: keyof CompactionPrompts;
  value: string;
  builtIn: string;
  problem: string | undefined;
  onChange: (text: string) => void;
};

/**
 * PromptField edits one prompt. Empty is the built-in prompt, which the box
 * shows faintly; the built-in one can be copied in as a starting point.
 */
function PromptField({ name, value, builtIn, problem, onChange }: PromptFieldProps) {
  const id = `compaction-prompt-${name}`;
  const { label, help } = promptLabels[name];
  const custom = value.trim() !== "";
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between gap-2">
        <Label htmlFor={id}>{label}</Label>
        <Button
          type="button"
          size="xs"
          variant="ghost"
          onClick={() => {
            onChange(custom ? "" : builtIn);
          }}
        >
          {custom ? "Use the built-in prompt" : "Edit the built-in prompt"}
        </Button>
      </div>
      <Textarea
        id={id}
        value={value}
        rows={4}
        placeholder={builtIn}
        className="max-h-64 font-mono text-xs"
        aria-invalid={problem !== undefined}
        aria-describedby={`${id}-help`}
        onChange={(e) => {
          onChange(e.target.value);
        }}
      />
      {problem !== undefined && <p className="text-destructive text-xs">{problem}</p>}
      <p id={`${id}-help`} className="text-muted-foreground text-xs">
        {help} {custom ? "A custom prompt is in use." : "Empty uses the built-in prompt."}
        {name !== "turn_prefix" && " A focus given with /compact is added after it."}
      </p>
    </div>
  );
}
