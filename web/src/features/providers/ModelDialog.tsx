/** The form that changes one model: its name, its identifier, its limits, and its reasoning. */

import { Plus, X } from "lucide-react";
import { useState } from "react";

import type { Model, ReasoningEffort } from "@/api/types";
import { ActionError } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { commonEfforts, effortProblem } from "@/features/providers/efforts";
import { FieldProblem, NumberField } from "@/features/providers/ModelPicker";
import { useModels, useUpdateModel } from "@/features/providers/queries";
import { cn } from "@/lib/utils";

export type ModelDialogProps = {
  /** model is the model being changed; null closes the dialog. */
  model: Model | null;
  onOpenChange: (open: boolean) => void;
};

/** endpointDefault stands for the empty effort, which a chip cannot hold. */
const endpointDefault = "";

export function ModelDialog({ model, onOpenChange }: ModelDialogProps) {
  return (
    <Dialog
      open={model !== null}
      onOpenChange={(open) => {
        if (!open) onOpenChange(false);
      }}
    >
      <DialogContent className="sm:max-w-lg">
        {model && (
          <ModelForm
            key={model.id}
            model={model}
            onDone={() => {
              onOpenChange(false);
            }}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

function ModelForm({ model, onDone }: { model: Model; onDone: () => void }) {
  const update = useUpdateModel();
  const models = useModels();
  const takenNames = (models.data?.models ?? [])
    .filter((m) => m.id !== model.id)
    .map((m) => m.name);
  const [name, setName] = useState(model.name);
  const [id, setId] = useState(model.model);
  const [window, setWindow] = useState(model.context_window);
  const [output, setOutput] = useState(model.max_output);
  const [effort, setEffort] = useState<ReasoningEffort>(model.reasoning_effort ?? endpointDefault);
  const [efforts, setEfforts] = useState<ReasoningEffort[]>(model.reasoning_efforts);
  const [preserve, setPreserve] = useState(model.preserve_thinking);

  // Each problem belongs to one field and is shown right under it.
  type Field = "name" | "id" | "window" | "output";
  const problem: { field: Field; message: string } | null =
    name.trim() === ""
      ? { field: "name", message: "Give the model a name." }
      : takenNames.includes(name.trim())
        ? {
            field: "name",
            message: `Another model is called “${name.trim()}”; choose a different name.`,
          }
        : id.trim() === ""
          ? {
              field: "id",
              message: "The identifier is what the endpoint calls the model; it cannot be empty.",
            }
          : !Number.isInteger(window) || !(window > 0)
            ? { field: "window", message: "Enter a whole number of tokens, 1 or more." }
            : !Number.isInteger(output) || !(output > 0)
              ? { field: "output", message: "Enter a whole number of tokens, 1 or more." }
              : output > window
                ? {
                    field: "output",
                    message: "Lower the max output: it cannot be larger than the context window.",
                  }
                : null;
  const problemFor = (field: Field) => (problem?.field === field ? problem.message : undefined);

  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        if (problem) return;
        update.mutate(
          {
            id: model.id,
            input: {
              name: name.trim(),
              model: id.trim(),
              context_window: window,
              max_output: output,
              reasoning_effort: effort,
              reasoning_efforts: efforts,
              preserve_thinking: preserve,
            },
          },
          { onSuccess: onDone },
        );
      }}
    >
      <DialogHeader>
        <DialogTitle>Edit {model.name}</DialogTitle>
        <DialogDescription>
          The name is what Eika and its agents call the model; the identifier is what the
          provider&apos;s endpoint calls it.
        </DialogDescription>
      </DialogHeader>

      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <Label htmlFor="model-name" className="text-xs">
            Name in Eika
          </Label>
          <Input
            id="model-name"
            value={name}
            aria-invalid={problemFor("name") !== undefined}
            aria-describedby={problemFor("name") ? "model-name-problem" : undefined}
            onChange={(e) => {
              setName(e.target.value);
            }}
          />
          <FieldProblem id="model-name-problem" message={problemFor("name")} />
        </div>
        <div className="space-y-1">
          <Label htmlFor="model-id" className="text-xs">
            Identifier
          </Label>
          <Input
            id="model-id"
            value={id}
            className="font-mono"
            aria-invalid={problemFor("id") !== undefined}
            aria-describedby={problemFor("id") ? "model-id-problem" : undefined}
            onChange={(e) => {
              setId(e.target.value);
            }}
          />
          <FieldProblem id="model-id-problem" message={problemFor("id")} />
        </div>
        <NumberField
          id="model-window"
          label="Context window"
          value={window}
          problem={problemFor("window")}
          onChange={setWindow}
        />
        <NumberField
          id="model-output"
          label="Max output"
          value={output}
          problem={problemFor("output")}
          onChange={setOutput}
        />
      </div>

      <EffortField
        effort={effort}
        efforts={efforts}
        onEffortChange={setEffort}
        onEffortsChange={(next) => {
          setEfforts(next);
          // An effort the list no longer holds cannot stay in force: the
          // status bar cycles through this list and would never return to it.
          if (effort !== endpointDefault && !next.includes(effort)) setEffort(endpointDefault);
        }}
      />

      <div className="flex items-start justify-between gap-4 rounded-md border p-3">
        <div className="space-y-0.5">
          <Label htmlFor="model-preserve" className="text-sm">
            Preserve reasoning between turns
          </Label>
          <p className="text-muted-foreground text-xs">
            For endpoints that stream reasoning_content and expect it back, such as DeepSeek, Qwen,
            and many vLLM servers. The official OpenAI API does not accept it.
          </p>
        </div>
        <Switch id="model-preserve" checked={preserve} onCheckedChange={setPreserve} />
      </div>

      {problem !== null && (
        <p className="text-destructive text-xs">Fix the field marked above to save.</p>
      )}
      {update.isError && <ActionError action={`save ${model.name}`} error={update.error} />}

      <DialogFooter>
        <Button type="button" variant="ghost" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={problem !== null || update.isPending}>
          {update.isPending ? "Saving…" : "Save"}
        </Button>
      </DialogFooter>
    </form>
  );
}

/**
 * EffortField is the model's reasoning vocabulary: the words this endpoint
 * accepts, and which of them is in force. The status bar cycles through the
 * list in this order, so the order is the user's.
 */
function EffortField({
  effort,
  efforts,
  onEffortChange,
  onEffortsChange,
}: {
  effort: ReasoningEffort;
  efforts: readonly ReasoningEffort[];
  onEffortChange: (effort: ReasoningEffort) => void;
  onEffortsChange: (efforts: ReasoningEffort[]) => void;
}) {
  const [draft, setDraft] = useState("");
  const [problem, setProblem] = useState<string | null>(null);

  const add = () => {
    const why = effortProblem(draft, efforts);
    setProblem(why);
    if (why !== null) return;
    const added = draft.trim();
    onEffortsChange([...efforts, added]);
    // The first word a model is given is the one it should use.
    if (efforts.length === 0) onEffortChange(added);
    setDraft("");
  };

  const remove = (gone: ReasoningEffort) => {
    onEffortsChange(efforts.filter((e) => e !== gone));
    setProblem(null);
  };

  return (
    <div className="space-y-2">
      <Label htmlFor="model-effort">Reasoning efforts</Label>
      <p className="text-muted-foreground text-xs">
        Sent as reasoning_effort. Add the words this endpoint accepts; the session&apos;s status bar
        cycles through them in this order. Pick the one a new run starts on.
      </p>
      <div className="flex flex-wrap gap-1.5">
        <EffortChip
          label="Endpoint default"
          chosen={effort === endpointDefault}
          onChoose={() => {
            onEffortChange(endpointDefault);
          }}
        />
        {efforts.map((option) => (
          <EffortChip
            key={option}
            label={option}
            mono
            chosen={effort === option}
            onChoose={() => {
              onEffortChange(option);
            }}
            onRemove={() => {
              remove(option);
            }}
          />
        ))}
      </div>
      <div className="flex gap-1.5">
        <Input
          id="model-effort"
          value={draft}
          list="model-effort-suggestions"
          placeholder="high"
          className="font-mono"
          aria-label="Add a reasoning effort"
          aria-invalid={problem !== null}
          aria-describedby={problem === null ? undefined : "model-effort-problem"}
          onChange={(e) => {
            setDraft(e.target.value);
            setProblem(null);
          }}
          onKeyDown={(e) => {
            if (e.key !== "Enter") return;
            // The field sits inside the model form; Enter adds a word here
            // rather than saving the model.
            e.preventDefault();
            add();
          }}
        />
        <datalist id="model-effort-suggestions">
          {commonEfforts.map((option) => (
            <option key={option} value={option} />
          ))}
        </datalist>
        <Button type="button" variant="outline" onClick={add}>
          <Plus aria-hidden />
          Add
        </Button>
      </div>
      <FieldProblem id="model-effort-problem" message={problem ?? undefined} />
    </div>
  );
}

/** EffortChip is one word in the model's vocabulary, and whether it is in force. */
function EffortChip({
  label,
  chosen,
  mono = false,
  onChoose,
  onRemove,
}: {
  label: string;
  chosen: boolean;
  mono?: boolean;
  onChoose: () => void;
  onRemove?: () => void;
}) {
  return (
    <span
      className={cn(
        "flex items-center rounded-md border text-xs transition-colors",
        chosen ? "border-primary bg-primary/10 text-foreground" : "hover:bg-accent",
      )}
    >
      <button
        type="button"
        aria-pressed={chosen}
        className={cn("py-1 pl-2", onRemove === undefined && "pr-2", mono && "font-mono")}
        onClick={onChoose}
      >
        {label}
      </button>
      {onRemove && (
        <button
          type="button"
          aria-label={`Remove ${label}`}
          className="hover:text-destructive py-1 pr-1.5 pl-1"
          onClick={onRemove}
        >
          <X aria-hidden className="size-3" />
        </button>
      )}
    </span>
  );
}
