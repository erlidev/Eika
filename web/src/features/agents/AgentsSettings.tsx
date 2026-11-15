/**
 * The Agents tab of the settings: Eika, the researcher and the worker, each
 * with the two things the user may change, its model class and its prompt.
 * Everything else about an agent, its tools and its limits, is fixed by its
 * definition. An unset field runs as built; the prompt can be compared with
 * the built-in one line by line.
 */

import { ChevronLeft, ChevronRight } from "lucide-react";
import { useState } from "react";

import type { Agent, AgentOverride, ModelClassName } from "@/api/types";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { ActionError, LoadError, Notice } from "@/components/Notice";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ChoiceGroup, FieldHeader, ResetButton } from "@/features/agents/fields";
import { textProblem } from "@/lib/textLimit";
import { PromptEditor } from "@/features/agents/PromptEditor";
import { useAgents, useResetAgent, useSaveAgent } from "@/features/agents/queries";
import { classInfo } from "@/features/settings/classes";
import { useSettings } from "@/features/settings/queries";

/** AgentClass is a class an agent may be given; the decision class serves no agent. */
type AgentClass = Exclude<ModelClassName, "decision">;

const classOptions: readonly { value: AgentClass; label: string }[] = [
  { value: "main", label: classInfo.main.label },
  { value: "fast", label: classInfo.fast.label },
  { value: "deep", label: classInfo.deep.label },
];

export function AgentsSettings() {
  const agents = useAgents();
  const [selected, setSelected] = useState("");
  if (agents.isPending) return <Notice tone="pending">Loading the agents…</Notice>;
  if (agents.isError) {
    return (
      <LoadError
        what="the agents"
        error={agents.error}
        retrying={agents.isFetching}
        retry={() => void agents.refetch()}
      />
    );
  }
  const open = agents.data.find((a) => a.name === selected);
  if (open !== undefined) {
    return (
      <AgentEditor
        key={open.name}
        agent={open}
        onBack={() => {
          setSelected("");
        }}
      />
    );
  }
  return <AgentList agents={agents.data} onOpen={setSelected} />;
}

type AgentListProps = { agents: readonly Agent[]; onOpen: (name: string) => void };

function AgentList({ agents, onOpen }: AgentListProps) {
  return (
    <div className="space-y-4">
      <p className="text-muted-foreground text-sm">
        Eika and the subagents it delegates to. Each runs on a model class, which General maps to a
        model, and a prompt. Change either here; the tools and limits belong to the agent and are
        fixed.
      </p>
      <ul className="divide-y overflow-hidden rounded-md border" aria-label="Agents">
        {agents.map((agent) => (
          <li key={agent.name}>
            <button
              type="button"
              onClick={() => {
                onOpen(agent.name);
              }}
              className="hover:bg-accent focus-visible:ring-ring flex w-full items-center gap-3 px-3 py-2.5 text-left transition-colors focus-visible:ring-1 focus-visible:outline-none focus-visible:ring-inset"
            >
              <div className="min-w-40 flex-1">
                <p className="flex min-w-0 items-center gap-2">
                  <span className="truncate text-sm font-medium">{agent.name}</span>
                  <Badge variant="secondary" className="h-4 shrink-0 px-1 text-2xs">
                    {agent.class}
                  </Badge>
                  {changes(agent) && (
                    <Badge variant="outline" className="h-4 shrink-0 px-1 text-2xs">
                      changed
                    </Badge>
                  )}
                </p>
                <p className="text-muted-foreground truncate text-xs">{agent.description}</p>
              </div>
              <ChevronRight aria-hidden className="text-muted-foreground size-4 shrink-0" />
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** changes says whether the user has overridden anything of the agent. */
function changes(agent: Agent): boolean {
  return agent.overrides.class !== null || agent.overrides.prompt !== null;
}

type AgentEditorProps = { agent: Agent; onBack: () => void };

function AgentEditor({ agent, onBack }: AgentEditorProps) {
  const save = useSaveAgent();
  const reset = useResetAgent();
  const settings = useSettings();
  const [cls, setCls] = useState<AgentClass | null>(agent.overrides.class);
  const [prompt, setPrompt] = useState<string | null>(agent.overrides.prompt);
  const [confirming, setConfirming] = useState(false);
  const problem = prompt === null ? "" : textProblem(prompt);
  const dirty = cls !== agent.overrides.class || prompt !== agent.overrides.prompt;
  const saving = save.isPending;
  const running = settings.data?.classes.find((c) => c.class === (cls ?? agent.builtin.class));

  const submit = () => {
    const override: AgentOverride = { class: cls, prompt };
    save.mutate({ name: agent.name, override });
  };

  return (
    <form
      className="space-y-5"
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
    >
      <Button type="button" size="xs" variant="ghost" className="-ml-2" onClick={onBack}>
        <ChevronLeft aria-hidden />
        All agents
      </Button>
      <div className="space-y-1">
        <h3 className="text-sm font-medium">{agent.name}</h3>
        <p className="text-muted-foreground text-xs">{agent.description}</p>
        <p className="text-muted-foreground text-xs">
          Tools, fixed by the agent:{" "}
          <span className="text-foreground font-mono">{agent.tools.join(", ")}</span>
        </p>
      </div>

      <div className="space-y-1.5">
        <FieldHeader
          id={`agent-${agent.name}-class-label`}
          label="Model class"
          set={cls !== null}
          from="default"
        >
          {cls !== null && (
            <ResetButton
              label="the model class"
              onClick={() => {
                setCls(null);
              }}
            />
          )}
        </FieldHeader>
        <ChoiceGroup
          labelledBy={`agent-${agent.name}-class-label`}
          options={classOptions}
          value={cls}
          inherited={agent.builtin.class}
          onChange={setCls}
        />
        <p className="text-muted-foreground text-xs">
          {cls === null ? `Built in: ${agent.builtin.class}. ` : ""}
          {running === undefined || running.model === ""
            ? "No model serves this class yet; General maps classes to models."
            : `Served by ${running.model}.`}
        </p>
      </div>

      <PromptEditor
        id={`agent-${agent.name}-prompt`}
        label="Prompt"
        hint="The base system prompt. Instructions and the context preamble follow it."
        value={prompt}
        inherited={agent.builtin.prompt}
        from="default"
        builtin={agent.builtin.prompt}
        onChange={setPrompt}
      />
      {problem !== "" && <Notice tone="error">{problem}</Notice>}

      {save.isError && <ActionError action={`save ${agent.name}`} error={save.error} />}
      {reset.isError && <ActionError action={`reset ${agent.name}`} error={reset.error} />}
      {save.isSuccess && !dirty && <Notice tone="success">Saved. The next run uses it.</Notice>}
      <div className="flex flex-wrap items-center justify-end gap-2 border-t pt-3">
        <Button
          type="button"
          variant="outline"
          className="mr-auto"
          disabled={!changes(agent) || reset.isPending}
          onClick={() => {
            setConfirming(true);
          }}
        >
          Reset to built-in
        </Button>
        <Button type="button" variant="ghost" onClick={onBack}>
          Back
        </Button>
        <Button type="submit" disabled={saving || !dirty || problem !== ""}>
          {saving ? "Saving…" : "Save agent"}
        </Button>
      </div>
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={`Reset ${agent.name}?`}
        description="Its class and prompt go back to the built-in ones, and any change you have not saved is lost. The next run uses them."
        confirmLabel="Reset"
        onConfirm={() => {
          reset.mutate(agent.name, {
            onSuccess: () => {
              setCls(null);
              setPrompt(null);
              save.reset();
            },
          });
        }}
      />
    </form>
  );
}
