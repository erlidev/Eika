/**
 * Every provider and its models, with the actions that change them: add a
 * provider, add models to one, test, edit, delete, and choose the default.
 * It is the Models tab of the settings dialog.
 */

import { KeyRound, Pencil, Plus, Star, Trash2 } from "lucide-react";
import { useState } from "react";

import type { Model, Provider } from "@/api/types";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { ActionError, LoadError, Notice } from "@/components/Notice";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ModelDialog } from "@/features/providers/ModelDialog";
import { ModelPicker } from "@/features/providers/ModelPicker";
import { presetOf } from "@/features/providers/presets";
import { ProviderForm } from "@/features/providers/ProviderForm";
import {
  useDeleteModel,
  useDeleteProvider,
  useModels,
  useProviders,
  useTestModel,
} from "@/features/providers/queries";
import { formatTokens } from "@/lib/format";

export type ModelsPanelProps = {
  /** onDefaultChange makes a model the default; the settings feature owns that write. */
  onDefaultChange: (name: string) => void;
  /** defaultError is why the last change of the default model failed. */
  defaultError?: Error | undefined;
};

export function ModelsPanel({ onDefaultChange, defaultError }: ModelsPanelProps) {
  const providers = useProviders();
  const models = useModels();
  const [addingProvider, setAddingProvider] = useState(false);
  const [editingProvider, setEditingProvider] = useState<Provider | null>(null);
  const [pickingFor, setPickingFor] = useState<Provider | null>(null);
  const [editingModel, setEditingModel] = useState<Model | null>(null);

  const list = providers.data?.providers ?? [];
  const all = models.data?.models ?? [];

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-4">
        <p className="text-muted-foreground text-sm">
          Any OpenAI-compatible endpoint works: hosted APIs, OpenRouter, or a model server on this
          machine. Keys are encrypted at rest.
        </p>
        <Button
          size="sm"
          onClick={() => {
            setAddingProvider(true);
          }}
        >
          <Plus aria-hidden />
          Add provider
        </Button>
      </div>

      {providers.isPending && <Notice tone="pending">Loading the providers…</Notice>}
      {providers.isError && (
        <LoadError
          what="the providers"
          error={providers.error}
          retrying={providers.isFetching}
          retry={() => void providers.refetch()}
        />
      )}
      {models.isError && (
        <LoadError
          what="the models, so none are listed below"
          error={models.error}
          retrying={models.isFetching}
          retry={() => void models.refetch()}
        />
      )}
      {defaultError !== undefined && (
        <ActionError action="change the default model" error={defaultError} />
      )}
      {providers.isSuccess && list.length === 0 && (
        <div className="rounded-md border border-dashed p-8 text-center">
          <p className="text-sm font-medium">No providers yet</p>
          <p className="text-muted-foreground mt-1 text-sm">
            Add one to give agents a model to run on.
          </p>
        </div>
      )}

      <ul className="space-y-3">
        {list.map((provider) => (
          <ProviderCard
            key={provider.id}
            provider={provider}
            models={all.filter((m) => m.provider_id === provider.id)}
            modelsLoaded={models.isSuccess}
            defaultModel={models.data?.default ?? ""}
            onEdit={() => {
              setEditingProvider(provider);
            }}
            onAddModels={() => {
              setPickingFor(provider);
            }}
            onEditModel={setEditingModel}
            onDefaultChange={onDefaultChange}
          />
        ))}
      </ul>

      <Dialog open={addingProvider} onOpenChange={setAddingProvider}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>Add a provider</DialogTitle>
            <DialogDescription>
              Choose where the models come from. You pick the models next.
            </DialogDescription>
          </DialogHeader>
          <ProviderForm
            onCancel={() => {
              setAddingProvider(false);
            }}
            onSaved={(provider) => {
              setAddingProvider(false);
              setPickingFor(provider);
            }}
            submitLabel="Add and choose models"
          />
        </DialogContent>
      </Dialog>

      <Dialog
        open={editingProvider !== null}
        onOpenChange={(open) => {
          if (!open) setEditingProvider(null);
        }}
      >
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>Edit {editingProvider?.name}</DialogTitle>
            <DialogDescription>
              Changes apply to the next run; a run already going keeps its connection.
            </DialogDescription>
          </DialogHeader>
          {editingProvider && (
            <ProviderForm
              key={editingProvider.id}
              provider={editingProvider}
              onCancel={() => {
                setEditingProvider(null);
              }}
              onSaved={() => {
                setEditingProvider(null);
              }}
            />
          )}
        </DialogContent>
      </Dialog>

      <Dialog
        open={pickingFor !== null}
        onOpenChange={(open) => {
          if (!open) setPickingFor(null);
        }}
      >
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>Add models from {pickingFor?.name}</DialogTitle>
            <DialogDescription>
              Tick the models agents may use, or type one by name.
            </DialogDescription>
          </DialogHeader>
          {pickingFor && (
            <ModelPicker
              key={pickingFor.id}
              provider={pickingFor}
              onCancel={() => {
                setPickingFor(null);
              }}
              onDone={() => {
                setPickingFor(null);
              }}
            />
          )}
        </DialogContent>
      </Dialog>

      <ModelDialog
        model={editingModel}
        onOpenChange={() => {
          setEditingModel(null);
        }}
      />
    </div>
  );
}

type ProviderCardProps = {
  provider: Provider;
  models: Model[];
  /** modelsLoaded is false while the model list is loading or failed, so no list reads as empty. */
  modelsLoaded: boolean;
  defaultModel: string;
  onEdit: () => void;
  onAddModels: () => void;
  onEditModel: (model: Model) => void;
  onDefaultChange: (name: string) => void;
  /** defaultError is why the last change of the default model failed. */
  defaultError?: string | undefined;
};

function ProviderCard({
  provider,
  models,
  modelsLoaded,
  defaultModel,
  onEdit,
  onAddModels,
  onEditModel,
  onDefaultChange,
}: ProviderCardProps) {
  const remove = useDeleteProvider();
  const [confirming, setConfirming] = useState(false);
  const preset = presetOf(provider.base_url);

  return (
    <li className="overflow-hidden rounded-md border">
      <div className="bg-muted/40 flex flex-wrap items-center gap-x-3 gap-y-1 border-b px-4 py-2.5">
        {/* A name column narrower than 10rem wraps the actions below it
            instead of cutting the name to a letter or two. */}
        <div className="min-w-40 flex-1">
          <p className="truncate text-sm font-medium">{provider.name}</p>
          <p className="text-muted-foreground truncate font-mono text-xs">{provider.base_url}</p>
        </div>
        <span className="text-muted-foreground flex items-center gap-1 text-xs">
          <KeyRound aria-hidden className="size-3.5" />
          {provider.api_key_set ? (
            provider.api_key_hint ? (
              <span className="font-mono">…{provider.api_key_hint}</span>
            ) : (
              "key stored"
            )
          ) : preset?.keyRequired === false ? (
            "no key needed"
          ) : preset?.keyRequired ? (
            "no key: edit the provider to add one"
          ) : (
            "no key"
          )}
        </span>
        <span className="flex items-center gap-1">
          <Button size="xs" variant="outline" onClick={onAddModels}>
            <Plus aria-hidden />
            Models
          </Button>
          <Button
            size="icon-xs"
            variant="ghost"
            aria-label={`Edit ${provider.name}`}
            onClick={onEdit}
          >
            <Pencil aria-hidden />
          </Button>
          <Button
            size="icon-xs"
            variant="ghost"
            aria-label={`Delete ${provider.name}`}
            onClick={() => {
              setConfirming(true);
            }}
          >
            <Trash2 aria-hidden />
          </Button>
        </span>
      </div>
      {!modelsLoaded ? null : models.length === 0 ? (
        <p className="text-muted-foreground px-4 py-3 text-xs">
          No models from this provider yet.{" "}
          <button type="button" className="underline underline-offset-4" onClick={onAddModels}>
            Add some
          </button>
          .
        </p>
      ) : (
        <ul className="divide-y">
          {models.map((model) => (
            <ModelRow
              key={model.id}
              model={model}
              isDefault={model.name === defaultModel}
              onEdit={() => {
                onEditModel(model);
              }}
              onMakeDefault={() => {
                onDefaultChange(model.name);
              }}
            />
          ))}
        </ul>
      )}
      {remove.isError && <ActionError action={`delete ${provider.name}`} error={remove.error} />}
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={`Delete ${provider.name}?`}
        description={`Its key and its ${String(models.length)} model${models.length === 1 ? "" : "s"} are removed. Sessions keep their history; runs already going finish on the connection they have.`}
        confirmLabel="Delete provider"
        onConfirm={() => {
          remove.mutate(provider.id);
        }}
      />
    </li>
  );
}

type ModelRowProps = {
  model: Model;
  isDefault: boolean;
  onEdit: () => void;
  onMakeDefault: () => void;
};

function ModelRow({ model, isDefault, onEdit, onMakeDefault }: ModelRowProps) {
  const remove = useDeleteModel();
  const test = useTestModel();
  const [confirming, setConfirming] = useState(false);

  return (
    <li className="px-4 py-2.5">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <div className="min-w-40 flex-1">
          <p className="flex min-w-0 items-center gap-2 text-sm">
            <span className="truncate font-medium">{model.name}</span>
            {isDefault && (
              <Badge variant="secondary" className="shrink-0">
                Default
              </Badge>
            )}
            {model.reasoning_effort !== undefined && model.reasoning_effort !== "" && (
              <Badge variant="outline" className="shrink-0">
                {model.reasoning_effort}
              </Badge>
            )}
          </p>
          <p className="text-muted-foreground truncate font-mono text-xs">
            {model.model !== model.name && `${model.model} · `}
            {formatTokens(model.context_window)} context · {formatTokens(model.max_output)} out
          </p>
        </div>
        <span className="flex items-center gap-1">
          {!isDefault && (
            <Button size="xs" variant="ghost" onClick={onMakeDefault}>
              <Star aria-hidden />
              Make default
            </Button>
          )}
          <Button
            size="xs"
            variant="ghost"
            disabled={test.isPending}
            onClick={() => {
              test.mutate({
                provider_id: model.provider_id,
                model: model.model,
                ...(model.reasoning_effort ? { reasoning_effort: model.reasoning_effort } : {}),
                thinking_switch: model.thinking_switch,
                preserve_thinking: model.preserve_thinking,
              });
            }}
          >
            {test.isPending ? "Testing…" : "Test"}
          </Button>
          <Button size="icon-xs" variant="ghost" aria-label={`Edit ${model.name}`} onClick={onEdit}>
            <Pencil aria-hidden />
          </Button>
          <Button
            size="icon-xs"
            variant="ghost"
            aria-label={`Delete ${model.name}`}
            onClick={() => {
              setConfirming(true);
            }}
          >
            <Trash2 aria-hidden />
          </Button>
        </span>
      </div>
      {test.isSuccess && (
        <Notice tone="success" className="mt-2">
          Answered in {(test.data.latency_ms / 1000).toFixed(1)}s
          {test.data.reply !== "" ? `: “${test.data.reply.slice(0, 80)}”` : "."}
        </Notice>
      )}
      {test.isError && (
        <ActionError action={`test ${model.name}`} error={test.error} className="mt-2" />
      )}
      {remove.isError && (
        <ActionError action={`delete ${model.name}`} error={remove.error} className="mt-2" />
      )}
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={`Delete ${model.name}?`}
        description={
          isDefault
            ? "It is the default model; the first remaining model becomes the default until you pick another."
            : "Runs can no longer choose it. Sessions keep their history."
        }
        confirmLabel="Delete model"
        onConfirm={() => {
          remove.mutate(model.id);
        }}
      />
    </li>
  );
}
