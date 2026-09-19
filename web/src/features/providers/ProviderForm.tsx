/**
 * The form that connects a model provider: pick a well-known endpoint or type
 * one, give it a key, and check that the endpoint answers before saving. The
 * setup wizard and the settings dialog both use it, for a new provider and
 * for changing one.
 */

import { ExternalLink } from "lucide-react";
import { useState } from "react";

import type { Provider } from "@/api/types";
import { Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  customPresetId,
  presetOf,
  presets,
  uniqueProviderName,
} from "@/features/providers/presets";
import type { ProviderPreset } from "@/features/providers/presets";
import {
  useCreateProvider,
  useProbeProvider,
  useProviders,
  useUpdateProvider,
} from "@/features/providers/queries";
import { cn } from "@/lib/utils";

export type ProviderFormProps = {
  /** provider is the provider being changed; absent for a new one. */
  provider?: Provider;
  /** onSaved receives the provider as the harness stored it. */
  onSaved: (provider: Provider) => void;
  /** onCancel shows a cancel button when set. */
  onCancel?: () => void;
  submitLabel?: string;
};

export function ProviderForm({ provider, onSaved, onCancel, submitLabel }: ProviderFormProps) {
  const providers = useProviders();
  const create = useCreateProvider();
  const update = useUpdateProvider();
  const probe = useProbeProvider();
  const editing = provider !== undefined;
  const takenNames = (providers.data?.providers ?? [])
    .filter((p) => p.id !== provider?.id)
    .map((p) => p.name);

  const initialPreset = editing ? presetOf(provider.base_url) : presets[0];
  const [presetId, setPresetId] = useState(initialPreset?.id ?? customPresetId);
  const [name, setName] = useState(provider?.name ?? initialPreset?.name ?? "");
  const [baseUrl, setBaseUrl] = useState(provider?.base_url ?? initialPreset?.baseUrl ?? "");
  const [apiKey, setApiKey] = useState("");
  const [removeKey, setRemoveKey] = useState(false);
  const preset = presets.find((p) => p.id === presetId);

  const choose = (next: ProviderPreset) => {
    setPresetId(next.id);
    setBaseUrl(next.baseUrl);
    setName(next.id === customPresetId ? "" : uniqueProviderName(next.name, takenNames));
    probe.reset();
  };

  const problem = validate({
    name,
    baseUrl,
    apiKey,
    takenNames,
    keyRequired: !editing && (preset?.keyRequired ?? false),
  });

  const test = () => {
    probe.mutate({
      ...(editing ? { provider_id: provider.id } : {}),
      base_url: baseUrl.trim(),
      // Editing with the key field blank tests the key that is stored.
      ...(editing && apiKey === "" && !removeKey ? {} : { api_key: apiKey.trim() }),
    });
  };

  const save = (e: React.SyntheticEvent) => {
    e.preventDefault();
    if (problem) return;
    if (editing) {
      update.mutate(
        {
          id: provider.id,
          input: {
            name: name.trim(),
            base_url: baseUrl.trim(),
            ...(apiKey !== "" ? { api_key: apiKey.trim() } : removeKey ? { api_key: "" } : {}),
          },
        },
        { onSuccess: onSaved },
      );
      return;
    }
    create.mutate(
      { name: name.trim(), base_url: baseUrl.trim(), api_key: apiKey.trim() },
      { onSuccess: onSaved },
    );
  };

  const saving = create.isPending || update.isPending;
  const saveError = create.error ?? update.error;

  return (
    <form className="space-y-5" onSubmit={save}>
      {!editing && (
        <fieldset className="space-y-2">
          <legend className="text-sm font-medium">Provider</legend>
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
            {presets.map((option) => (
              <button
                key={option.id}
                type="button"
                aria-pressed={presetId === option.id}
                onClick={() => {
                  choose(option);
                }}
                className={cn(
                  "hover:bg-muted/60 focus-visible:ring-ring/50 rounded-lg border px-3 py-2 text-left transition-colors outline-none focus-visible:ring-3",
                  presetId === option.id && "border-primary bg-muted/60 ring-primary/20 ring-2",
                )}
              >
                <span className="block text-sm font-medium">{option.name}</span>
                <span className="text-muted-foreground block text-xs leading-snug">
                  {option.blurb}
                </span>
              </button>
            ))}
          </div>
        </fieldset>
      )}

      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-1.5">
          <Label htmlFor="provider-name">Name</Label>
          <Input
            id="provider-name"
            value={name}
            autoComplete="off"
            placeholder="My provider"
            onChange={(e) => {
              setName(e.target.value);
            }}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="provider-url">Base URL</Label>
          <Input
            id="provider-url"
            value={baseUrl}
            autoComplete="off"
            placeholder="https://api.example.com/v1"
            className="font-mono"
            onChange={(e) => {
              setBaseUrl(e.target.value);
              probe.reset();
            }}
          />
        </div>
      </div>

      <div className="space-y-1.5">
        <div className="flex items-baseline justify-between gap-2">
          <Label htmlFor="provider-key">API key</Label>
          {preset?.keyUrl !== undefined && (
            <a
              href={preset.keyUrl}
              target="_blank"
              rel="noreferrer"
              className="text-muted-foreground hover:text-foreground inline-flex items-center gap-1 text-xs underline-offset-4 hover:underline"
            >
              Get a key
              <ExternalLink aria-hidden className="size-3" />
            </a>
          )}
        </div>
        <Input
          id="provider-key"
          type="password"
          value={apiKey}
          autoComplete="off"
          className="font-mono"
          placeholder={
            editing && provider.api_key_set && !removeKey
              ? `stored key${provider.api_key_hint ? ` ending in ${provider.api_key_hint}` : ""}; leave blank to keep it`
              : preset?.keyRequired === false
                ? "not needed for a local server"
                : "sk-…"
          }
          onChange={(e) => {
            setApiKey(e.target.value);
            setRemoveKey(false);
            probe.reset();
          }}
        />
        <p className="text-muted-foreground text-xs">
          The key is encrypted before it is stored and never shown again.
          {editing && provider.api_key_set && !removeKey && (
            <>
              {" "}
              <button
                type="button"
                className="hover:text-foreground underline underline-offset-4"
                onClick={() => {
                  setRemoveKey(true);
                  setApiKey("");
                  probe.reset();
                }}
              >
                Remove the stored key
              </button>
            </>
          )}
        </p>
      </div>

      {preset?.note !== undefined && <Notice>{preset.note}</Notice>}

      {probe.isPending && (
        <Notice tone="pending">Asking the endpoint which models it serves…</Notice>
      )}
      {probe.isSuccess && (
        <Notice tone="success">
          Connected.{" "}
          {probe.data.length === 0
            ? "The endpoint lists no models; add them by name next."
            : `${String(probe.data.length)} model${probe.data.length === 1 ? "" : "s"} available.`}
        </Notice>
      )}
      {probe.isError && (
        <Notice tone="error">
          {probe.error.message}
          <span className="text-muted-foreground mt-1 block">
            Some endpoints do not list their models. You can still save and add models by name.
          </span>
        </Notice>
      )}

      {problem !== null && (name !== "" || baseUrl !== "") && (
        <p className="text-muted-foreground text-xs">{problem}</p>
      )}
      {saveError && <Notice tone="error">{saveError.message}</Notice>}

      <div className="flex flex-wrap items-center justify-end gap-2">
        {onCancel && (
          <Button type="button" variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
        )}
        <Button
          type="button"
          variant="outline"
          disabled={problem !== null || probe.isPending}
          onClick={test}
        >
          Test connection
        </Button>
        <Button type="submit" disabled={problem !== null || saving}>
          {saving ? "Saving…" : (submitLabel ?? (editing ? "Save" : "Add provider"))}
        </Button>
      </div>
    </form>
  );
}

type ProviderInput = {
  name: string;
  baseUrl: string;
  apiKey: string;
  takenNames: readonly string[];
  keyRequired: boolean;
};

/** validate mirrors what the harness refuses, so the save button says why it waits. */
function validate({
  name,
  baseUrl,
  apiKey,
  takenNames,
  keyRequired,
}: ProviderInput): string | null {
  if (name.trim() === "") return "Give the provider a name.";
  if (takenNames.includes(name.trim())) return "Another provider has that name.";
  if (!/^https?:\/\/[^/\s]+/.test(baseUrl.trim())) {
    return "The base URL must start with http:// or https://.";
  }
  if (keyRequired && apiKey.trim() === "") return "This provider needs an API key.";
  return null;
}
