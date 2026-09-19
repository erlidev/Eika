/** The form that changes one model: its name, its identifier, its limits, and its reasoning. */

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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { FieldProblem, NumberField } from "@/features/providers/ModelPicker";
import { useModels, useUpdateModel } from "@/features/providers/queries";

export type ModelDialogProps = {
  /** model is the model being changed; null closes the dialog. */
  model: Model | null;
  onOpenChange: (open: boolean) => void;
};

/**
 * efforts are the reasoning_effort choices. A select item cannot have an
 * empty value, so "endpoint default" stands for the empty string.
 */
const efforts: readonly { value: string; effort: ReasoningEffort; label: string }[] = [
  { value: "default", effort: "", label: "Endpoint default" },
  { value: "none", effort: "none", label: "None" },
  { value: "minimal", effort: "minimal", label: "Minimal" },
  { value: "low", effort: "low", label: "Low" },
  { value: "medium", effort: "medium", label: "Medium" },
  { value: "high", effort: "high", label: "High" },
  { value: "xhigh", effort: "xhigh", label: "Extra high" },
  { value: "max", effort: "max", label: "Max" },
];

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
  const [effort, setEffort] = useState<ReasoningEffort>(model.reasoning_effort ?? "");
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

      <div className="space-y-1">
        <Label htmlFor="model-effort" className="text-xs">
          Reasoning effort
        </Label>
        <Select
          value={efforts.find((e) => e.effort === effort)?.value ?? "default"}
          onValueChange={(value) => {
            setEffort(efforts.find((e) => e.value === value)?.effort ?? "");
          }}
        >
          <SelectTrigger id="model-effort" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {efforts.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <p className="text-muted-foreground text-xs">
          Sent as reasoning_effort. Leave it to the endpoint unless the model supports it.
        </p>
      </div>

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
