/**
 * The Instructions tab of the settings: "About me" and the standing rules,
 * the two texts only the user writes. They go into the preamble every agent
 * session starts from; no agent tool can change them.
 */

import { useState } from "react";

import type { GlobalInstructions } from "@/api/types";
import { ActionError, LoadError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { useInstructions, useSaveInstructions } from "@/features/settings/queries";
import { formatTokens } from "@/lib/format";
import { textProblem } from "@/lib/textLimit";
import { estimateTokens } from "@/lib/tokens";

export function InstructionsSettings() {
  const instructions = useInstructions();
  const save = useSaveInstructions();
  if (instructions.isPending) return <Notice tone="pending">Loading the instructions…</Notice>;
  if (instructions.isError) {
    return (
      <LoadError
        what="the instructions"
        error={instructions.error}
        retrying={instructions.isFetching}
        retry={() => void instructions.refetch()}
      />
    );
  }
  return <InstructionsForm stored={instructions.data} save={save} />;
}

type InstructionsFormProps = {
  stored: GlobalInstructions;
  save: ReturnType<typeof useSaveInstructions>;
};

function InstructionsForm({ stored, save }: InstructionsFormProps) {
  const [aboutMe, setAboutMe] = useState(stored.about_me);
  const [rules, setRules] = useState(stored.rules);
  // A newer stored text, from a save or a refetch, replaces a field only if
  // the user has not changed it since the last one: a draft, or an edit made
  // while a save was pending, is never overwritten.
  const [seen, setSeen] = useState(stored);
  if (seen.about_me !== stored.about_me || seen.rules !== stored.rules) {
    setSeen(stored);
    if (aboutMe === seen.about_me) setAboutMe(stored.about_me);
    if (rules === seen.rules) setRules(stored.rules);
  }
  const dirty = aboutMe !== stored.about_me || rules !== stored.rules;
  const problems = { aboutMe: textProblem(aboutMe), rules: textProblem(rules) };
  const invalid = problems.aboutMe !== "" || problems.rules !== "";

  return (
    <form
      className="space-y-5"
      onSubmit={(e) => {
        e.preventDefault();
        if (!dirty || invalid) return;
        save.mutate({ about_me: aboutMe, rules });
      }}
    >
      <p className="text-muted-foreground text-sm">
        Text only you write. Eika and its subagents read both at the start of every conversation.
        They are followed, not enforced, and no agent can change them. A conversation already
        running keeps what it started with until its context is next compacted.
      </p>
      <TextField
        id="instructions-about-me"
        label="About me"
        hint="Who you are and what to know about you: your work, your tools, how you like answers."
        value={aboutMe}
        problem={problems.aboutMe}
        onChange={setAboutMe}
      />
      <TextField
        id="instructions-rules"
        label="Rules"
        hint="Standing rules for every conversation, such as how to write, what to ask first, what never to do."
        value={rules}
        problem={problems.rules}
        onChange={setRules}
      />
      {save.isError && <ActionError action="save the instructions" error={save.error} />}
      {save.isSuccess && !dirty && <Notice tone="success">Saved. New conversations use it.</Notice>}
      <div className="flex justify-end gap-2 border-t pt-3">
        <Button
          type="button"
          variant="ghost"
          disabled={!dirty}
          onClick={() => {
            setAboutMe(stored.about_me);
            setRules(stored.rules);
            save.reset();
          }}
        >
          Discard changes
        </Button>
        <Button type="submit" disabled={!dirty || invalid || save.isPending}>
          {save.isPending ? "Saving…" : "Save instructions"}
        </Button>
      </div>
    </form>
  );
}

type TextFieldProps = {
  id: string;
  label: string;
  hint: string;
  value: string;
  problem: string;
  onChange: (value: string) => void;
};

function TextField({ id, label, hint, value, problem, onChange }: TextFieldProps) {
  return (
    <div className="space-y-1.5">
      <div className="flex items-baseline gap-2">
        <Label htmlFor={id}>{label}</Label>
        <span
          className="text-muted-foreground font-mono text-2xs tabular-nums"
          title="Estimated tokens"
        >
          ~{formatTokens(estimateTokens(value.trim()))}
        </span>
      </div>
      <Textarea
        id={id}
        value={value}
        rows={6}
        aria-invalid={problem !== ""}
        aria-describedby={`${id}-hint`}
        className="max-h-80 min-h-32 font-mono text-xs leading-relaxed"
        onChange={(e) => {
          onChange(e.target.value);
        }}
      />
      <p id={`${id}-hint`} className="text-muted-foreground text-xs">
        {hint}
      </p>
      {problem !== "" && <p className="text-destructive text-xs">{problem}</p>}
    </div>
  );
}
