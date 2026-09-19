/**
 * Choosing which of a provider's models Eika may use. It lists what the
 * endpoint reports, lets the user tick models or type one by name, suggests
 * each model's limits, tests one on request, and adds the chosen ones.
 */

import { useQuery } from "@tanstack/react-query";
import { Plus, RefreshCw, Search, X } from "lucide-react";
import { useState } from "react";

import { probeProvider } from "@/api/routes";
import type { Model, ModelInfo, Provider } from "@/api/types";
import { LoadError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { chosenProblem, suggestLimits, suggestModelName } from "@/features/providers/limits";
import type { ModelChoice } from "@/features/providers/limits";
import { useCreateModel, useModels, useTestModel } from "@/features/providers/queries";
import { formatTokens } from "@/lib/format";

export type ModelPickerProps = {
  provider: Provider;
  /** onDone receives the models that were added. */
  onDone: (added: Model[]) => void;
  onCancel?: () => void;
  doneLabel?: string;
};

type Choice = ModelChoice;

/** filterThreshold is how many models a list has before it offers a search box. */
const filterThreshold = 8;

export function ModelPicker({ provider, onDone, onCancel, doneLabel }: ModelPickerProps) {
  const models = useModels();
  const create = useCreateModel();
  const discovered = useQuery({
    queryKey: ["providers", provider.id, "models"],
    queryFn: () => probeProvider({ provider_id: provider.id }),
    staleTime: 60_000,
  });
  const [chosen, setChosen] = useState<readonly Choice[]>([]);
  const [filter, setFilter] = useState("");
  const [typed, setTyped] = useState("");
  const [failure, setFailure] = useState("");
  const [adding, setAdding] = useState(false);

  const existing = models.data?.models ?? [];
  const onProvider = new Set(
    existing.filter((m) => m.provider_id === provider.id).map((m) => m.model),
  );
  const takenNames = [...existing.map((m) => m.name), ...chosen.map((c) => c.name)];
  const available = discovered.data ?? [];
  const shown = available.filter((m) => m.id.toLowerCase().includes(filter.trim().toLowerCase()));

  const pick = (id: string, info?: ModelInfo) => {
    if (chosen.some((c) => c.id === id)) return;
    setChosen([
      ...chosen,
      { id, name: suggestModelName(id, provider.name, takenNames), ...suggestLimits(id, info) },
    ]);
  };
  const drop = (id: string) => {
    setChosen(chosen.filter((c) => c.id !== id));
  };
  const change = (id: string, patch: Partial<Choice>) => {
    setChosen(chosen.map((c) => (c.id === id ? { ...c, ...patch } : c)));
  };

  const typedId = typed.trim();
  const typedProblem = onProvider.has(typedId)
    ? `${typedId} is already added from ${provider.name}.`
    : chosen.some((c) => c.id === typedId)
      ? `${typedId} is already in the list to add below.`
      : null;

  const addTyped = () => {
    const id = typed.trim();
    if (id === "" || typedProblem !== null) return;
    pick(
      id,
      available.find((m) => m.id === id),
    );
    setTyped("");
  };

  const problem = chosenProblem(chosen, existing);

  const addAll = async () => {
    setAdding(true);
    setFailure("");
    const added: Model[] = [];
    for (const choice of chosen) {
      try {
        added.push(
          await create.mutateAsync({
            provider_id: provider.id,
            name: choice.name.trim(),
            model: choice.id,
            context_window: choice.context_window,
            max_output: choice.max_output,
          }),
        );
      } catch (error) {
        setFailure(`${choice.id}: ${error instanceof Error ? error.message : String(error)}`);
        // What was added stays added; the rest stays chosen to retry.
        setChosen(chosen.filter((c) => !added.some((m) => m.model === c.id)));
        setAdding(false);
        return;
      }
    }
    setAdding(false);
    setChosen([]);
    onDone(added);
  };

  return (
    <div className="space-y-5">
      <section className="space-y-2" aria-label={`Models ${provider.name} serves`}>
        <div className="flex items-center justify-between gap-2">
          <h3 className="text-sm font-medium">Available from {provider.name}</h3>
          <Button
            type="button"
            size="xs"
            variant="ghost"
            disabled={discovered.isFetching}
            onClick={() => void discovered.refetch()}
          >
            <RefreshCw aria-hidden className={discovered.isFetching ? "animate-spin" : ""} />
            Refresh
          </Button>
        </div>

        {discovered.isPending && (
          <Notice tone="pending">Asking the endpoint for its models…</Notice>
        )}
        {discovered.isError && (
          <Notice tone="error">
            {discovered.error.message}
            <span className="text-muted-foreground mt-1 block">
              Type a model's identifier below to add it anyway.
            </span>
          </Notice>
        )}
        {discovered.isSuccess && available.length === 0 && (
          <Notice>The endpoint lists no models. Type a model&apos;s identifier below.</Notice>
        )}

        {available.length > filterThreshold && (
          <div className="relative">
            <Search
              aria-hidden
              className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2"
            />
            <Input
              aria-label="Filter models"
              placeholder={`Filter ${String(available.length)} models`}
              value={filter}
              className="pl-8"
              onChange={(e) => {
                setFilter(e.target.value);
              }}
            />
          </div>
        )}

        {available.length > 0 && (
          <ul className="max-h-64 divide-y overflow-y-auto rounded-lg border">
            {shown.map((info) => {
              const already = onProvider.has(info.id);
              const checked = already || chosen.some((c) => c.id === info.id);
              const inputId = `model-${provider.id}-${info.id}`;
              return (
                <li key={info.id} className="flex items-center gap-3 px-3 py-1.5">
                  <Checkbox
                    id={inputId}
                    checked={checked}
                    disabled={already}
                    onCheckedChange={(value) => {
                      if (value === true) pick(info.id, info);
                      else drop(info.id);
                    }}
                  />
                  <label
                    htmlFor={inputId}
                    className="min-w-0 flex-1 cursor-pointer truncate font-mono text-xs"
                  >
                    {info.id}
                  </label>
                  {already ? (
                    <span className="text-muted-foreground text-xs">added</span>
                  ) : (
                    info.context_window !== undefined &&
                    info.context_window > 0 && (
                      <span className="text-muted-foreground font-mono text-xs tabular-nums">
                        {formatTokens(info.context_window)}
                      </span>
                    )
                  )}
                </li>
              );
            })}
            {shown.length === 0 && (
              <li className="text-muted-foreground px-3 py-2 text-xs">Nothing matches.</li>
            )}
          </ul>
        )}

        <form
          className="flex gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            addTyped();
          }}
        >
          <Input
            aria-label="Model identifier"
            placeholder="or type a model identifier, such as gpt-5"
            value={typed}
            className="font-mono"
            onChange={(e) => {
              setTyped(e.target.value);
            }}
          />
          <Button
            type="submit"
            variant="outline"
            disabled={typedId === "" || typedProblem !== null}
          >
            <Plus aria-hidden />
            Add
          </Button>
        </form>
        {typedProblem !== null && <p className="text-muted-foreground text-xs">{typedProblem}</p>}
      </section>

      {chosen.length > 0 && (
        <section className="space-y-2" aria-label="Models to add">
          <h3 className="text-sm font-medium">To add</h3>
          <p className="text-muted-foreground text-xs">
            The limits are suggestions; check them against the provider&apos;s documentation. Runs
            refuse a request that cannot fit the context window.
          </p>
          <ul className="space-y-2">
            {chosen.map((choice) => (
              <ChoiceRow
                key={choice.id}
                provider={provider}
                choice={choice}
                onChange={(patch) => {
                  change(choice.id, patch);
                }}
                onRemove={() => {
                  drop(choice.id);
                }}
              />
            ))}
          </ul>
        </section>
      )}

      {models.isError && (
        <LoadError
          what="the models already added, so the names cannot be checked for duplicates yet"
          error={models.error}
          retrying={models.isFetching}
          retry={() => void models.refetch()}
        />
      )}
      {problem !== null && chosen.length > 0 && <Notice tone="error">{problem}</Notice>}
      {failure !== "" && <Notice tone="error">{failure}</Notice>}

      <div className="flex flex-wrap items-center justify-end gap-2">
        {onCancel && (
          <Button type="button" variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
        )}
        <Button
          type="button"
          disabled={chosen.length === 0 || problem !== null || !models.isSuccess || adding}
          onClick={() => void addAll()}
        >
          {adding
            ? "Adding…"
            : (doneLabel ?? `Add ${String(chosen.length)} model${chosen.length === 1 ? "" : "s"}`)}
        </Button>
      </div>
    </div>
  );
}

type ChoiceRowProps = {
  provider: Provider;
  choice: Choice;
  onChange: (patch: Partial<Choice>) => void;
  onRemove: () => void;
};

/** ChoiceRow is one model about to be added: its name, its limits, and a test. */
function ChoiceRow({ provider, choice, onChange, onRemove }: ChoiceRowProps) {
  const test = useTestModel();
  const base = `choice-${provider.id}-${choice.id}`;
  return (
    <li className="bg-muted/30 space-y-3 rounded-lg border p-3">
      <div className="flex items-center gap-2">
        <span className="min-w-0 flex-1 truncate font-mono text-xs font-medium">{choice.id}</span>
        <Button
          type="button"
          size="xs"
          variant="outline"
          disabled={test.isPending}
          onClick={() => {
            test.mutate({ provider_id: provider.id, model: choice.id });
          }}
        >
          {test.isPending ? "Testing…" : "Test"}
        </Button>
        <Button
          type="button"
          size="icon-xs"
          variant="ghost"
          aria-label={`Do not add ${choice.id}`}
          onClick={onRemove}
        >
          <X aria-hidden />
        </Button>
      </div>
      <div className="grid gap-3 sm:grid-cols-[2fr_1fr_1fr]">
        <div className="space-y-1">
          <Label htmlFor={`${base}-name`} className="text-xs">
            Name in Eika
          </Label>
          <Input
            id={`${base}-name`}
            value={choice.name}
            onChange={(e) => {
              onChange({ name: e.target.value });
            }}
          />
        </div>
        <NumberField
          id={`${base}-window`}
          label="Context window"
          value={choice.context_window}
          onChange={(context_window) => {
            onChange({ context_window });
          }}
        />
        <NumberField
          id={`${base}-output`}
          label="Max output"
          value={choice.max_output}
          onChange={(max_output) => {
            onChange({ max_output });
          }}
        />
      </div>
      {test.isSuccess && (
        <Notice tone="success">
          Answered in {(test.data.latency_ms / 1000).toFixed(1)}s
          {test.data.reply !== ""
            ? `: “${test.data.reply.slice(0, 80)}”`
            : " (no text: it spent its budget thinking)"}
        </Notice>
      )}
      {test.isError && <Notice tone="error">{test.error.message}</Notice>}
    </li>
  );
}

type NumberFieldProps = {
  id: string;
  label: string;
  value: number;
  onChange: (value: number) => void;
};

/** NumberField is a labelled token count. */
export function NumberField({ id, label, value, onChange }: NumberFieldProps) {
  return (
    <div className="space-y-1">
      <Label htmlFor={id} className="text-xs">
        {label}
      </Label>
      <Input
        id={id}
        type="number"
        min={1}
        step={1}
        inputMode="numeric"
        value={Number.isFinite(value) ? value : ""}
        className="font-mono tabular-nums"
        onChange={(e) => {
          // Not parseInt: "2.5" must stay visible as the mistake it is.
          onChange(e.target.value === "" ? Number.NaN : Number(e.target.value));
        }}
      />
    </div>
  );
}
