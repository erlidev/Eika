/**
 * The endpoint settings of one provider: what a probe found out about the
 * server behind it (its backend, how many requests it takes at once, how its
 * KV cache is divided) and the user's own say where the probe cannot know.
 * A value the user sets is kept when the endpoint is probed again, until it
 * is handed back to the probe.
 */

import { RefreshCw } from "lucide-react";
import { useState } from "react";

import { apis, backends, kvs } from "@/api/types";
import type { API, Backend, EndpointField, KV, Provider } from "@/api/types";
import { ActionError, Notice } from "@/components/Notice";
import { Badge } from "@/components/ui/badge";
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
import {
  apiLabels,
  backendLabels,
  conformanceSummary,
  endpointForm,
  endpointProblem,
  endpointUpdate,
  formatCount,
  formatProbed,
  kvLabels,
  maxPoolName,
} from "@/features/providers/endpoint";
import type { EndpointForm as Form } from "@/features/providers/endpoint";
import { FieldProblem, NumberField } from "@/features/providers/ModelPicker";
import { useProbeProvider, useProviders, useUpdateProvider } from "@/features/providers/queries";

export type EndpointDialogProps = {
  /** providerId is the provider whose endpoint is shown; null closes the dialog. */
  providerId: string | null;
  onClose: () => void;
};

export function EndpointDialog({ providerId, onClose }: EndpointDialogProps) {
  const providers = useProviders();
  const provider = providers.data?.providers.find((p) => p.id === providerId);
  return (
    <Dialog
      open={provider !== undefined}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent className="sm:max-w-lg">
        {provider && (
          // The form starts again when the values in force change, as a probe
          // or a reset does, and not when a probe finds the same ones.
          <EndpointForm
            key={`${provider.id}/${JSON.stringify(endpointForm(provider))}`}
            provider={provider}
            onDone={onClose}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

type EndpointFormProps = { provider: Provider; onDone: () => void };

function EndpointForm({ provider, onDone }: EndpointFormProps) {
  const update = useUpdateProvider();
  const probe = useProbeProvider();
  const [form, setForm] = useState<Form>(() => endpointForm(provider));
  const problem = endpointProblem(form);
  const change = endpointUpdate(provider, form);
  const dirty = Object.keys(change).length > 0;
  const set = (patch: Partial<Form>) => {
    setForm((current) => ({ ...current, ...patch }));
  };
  const busy = update.isPending || probe.isPending;

  const handBack = (field: EndpointField) => {
    // mutateAsync, not callbacks on mutate: the form starts again when the
    // values it shows change, and a callback of an unmounted form is dropped.
    update
      .mutateAsync({ id: provider.id, input: { reset: [field] } })
      .then(() => {
        probe.mutate({ provider_id: provider.id });
      })
      .catch(() => undefined);
  };
  const hint = (field: EndpointField) => (
    <Provenance
      field={field}
      provider={provider}
      disabled={busy}
      onHandBack={() => {
        handBack(field);
      }}
    />
  );

  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        if (problem !== null || !dirty) return;
        update
          .mutateAsync({ id: provider.id, input: change })
          .then(onDone)
          .catch(() => undefined);
      }}
    >
      <DialogHeader>
        <DialogTitle>Endpoint of {provider.name}</DialogTitle>
        <DialogDescription>
          What Eika found out about the server behind this provider. A value you set is kept when
          the endpoint is probed again. Probing also asks the provider&apos;s first model two small
          questions, to check the Responses API.
        </DialogDescription>
      </DialogHeader>

      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-muted-foreground text-xs">
          {provider.probed_at === undefined
            ? "Not probed yet."
            : `Probed ${formatProbed(provider.probed_at)}.`}
        </p>
        <Button
          type="button"
          size="xs"
          variant="outline"
          disabled={busy}
          onClick={() => {
            probe.mutate({ provider_id: provider.id });
          }}
        >
          <RefreshCw aria-hidden />
          {probe.isPending ? "Probing…" : "Probe again"}
        </Button>
      </div>
      {provider.probe_error !== undefined && provider.probe_error !== "" && (
        <Notice tone="error">
          The last probe failed, so the values below are from an earlier one: {provider.probe_error}
        </Notice>
      )}
      {probe.isError && <ActionError action="probe the endpoint" error={probe.error} />}

      <dl className="text-muted-foreground grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
        <dt>Window per request</dt>
        <dd className="font-mono tabular-nums">
          {provider.capabilities.window !== undefined && provider.capabilities.window > 0
            ? `${formatCount(provider.capabilities.window)} tokens`
            : "not stated by the server"}
        </dd>
        <dt>Counts tokens</dt>
        <dd>{countingText[provider.capabilities.counting]}</dd>
        <dt>Cache metrics</dt>
        <dd>
          {provider.capabilities.cache_metrics === "reported"
            ? "reported in every response"
            : "not known until a response says"}
        </dd>
      </dl>

      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <Label htmlFor="endpoint-backend" className="text-xs">
            Backend
          </Label>
          <Select
            value={form.backend}
            onValueChange={(next) => {
              const known = backends.find((b: Backend) => b === next);
              if (known) set({ backend: known });
            }}
          >
            <SelectTrigger id="endpoint-backend" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {backends.map((b) => (
                <SelectItem key={b} value={b}>
                  {backendLabels[b]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {hint("backend")}
        </div>

        <div className="space-y-1">
          <Label htmlFor="endpoint-api" className="text-xs">
            API
          </Label>
          <Select
            value={form.api}
            onValueChange={(next) => {
              const known = apis.find((a: API) => a === next);
              if (known) set({ api: known });
            }}
          >
            <SelectTrigger id="endpoint-api" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {apis.map((a) => (
                <SelectItem key={a} value={a}>
                  {apiLabels[a]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <p className="text-muted-foreground text-xs">
            {conformanceSummary(provider.capabilities.responses)}
          </p>
          {hint("api")}
        </div>

        <div className="space-y-1">
          <Label htmlFor="endpoint-pool" className="text-xs">
            Pool
          </Label>
          <Input
            id="endpoint-pool"
            value={form.pool}
            maxLength={maxPoolName + 1}
            aria-invalid={problem?.field === "pool"}
            aria-describedby="endpoint-pool-hint"
            onChange={(e) => {
              set({ pool: e.target.value });
            }}
          />
          <p id="endpoint-pool-hint" className="text-muted-foreground text-xs">
            Endpoints in one pool share hardware and take turns.
          </p>
          {problem?.field === "pool" && (
            <FieldProblem id="endpoint-pool-problem" message={problem.message} />
          )}
          {hint("pool")}
        </div>

        <div className="space-y-1">
          <NumberField
            id="endpoint-slots"
            label="Slots"
            value={form.slots}
            problem={problem?.field === "slots" ? problem.message : undefined}
            onChange={(slots) => {
              set({ slots });
            }}
          />
          <p className="text-muted-foreground text-xs">Requests the server takes at once.</p>
          {hint("slots")}
        </div>

        <div className="space-y-1">
          <Label htmlFor="endpoint-kv" className="text-xs">
            KV cache
          </Label>
          <Select
            value={form.kv}
            onValueChange={(next) => {
              const known = kvs.find((k: KV) => k === next);
              if (known) set({ kv: known });
            }}
          >
            <SelectTrigger id="endpoint-kv" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {kvs.map((k) => (
                <SelectItem key={k} value={k}>
                  {kvLabels[k]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {hint("kv")}
        </div>
      </div>

      {form.kv === "shared" && (
        <div className="space-y-1">
          <NumberField
            id="endpoint-kv-tokens"
            label="Shared cache size (tokens)"
            value={form.kvTokens}
            min={0}
            problem={problem?.field === "kvTokens" ? problem.message : undefined}
            onChange={(kvTokens) => {
              set({ kvTokens });
            }}
          />
          <p className="text-muted-foreground text-xs">
            One cache all requests draw on, so together they cannot exceed it. 0 means the size is
            not known.
          </p>
        </div>
      )}
      {form.kv === "partitioned" && (
        <p className="text-muted-foreground text-xs">
          Each slot has a fixed share of the cache, so a request can use only its slot&apos;s
          window.
        </p>
      )}
      {form.kv === "unknown" && (
        <p className="text-muted-foreground text-xs">
          The server does not say whether its cache is shared, and nothing it serves tells the two
          apart. Until you choose, Eika holds concurrent requests to one window&apos;s worth of
          tokens, which cannot overfill either kind. llama.cpp is shared with{" "}
          <span className="font-mono">--kv-unified</span> and partitioned without it.
        </p>
      )}

      {update.isError && <ActionError action={`save ${provider.name}`} error={update.error} />}
      {probe.isSuccess && (
        <Notice tone="success">
          Probed: {backendLabels[probe.data.probe.backend]}, {String(probe.data.probe.slots)} slot
          {probe.data.probe.slots === 1 ? "" : "s"}, {apiLabels[probe.data.probe.api]}.
        </Notice>
      )}

      <DialogFooter>
        <Button type="button" variant="ghost" onClick={onDone}>
          Close
        </Button>
        <Button type="submit" disabled={problem !== null || !dirty || busy}>
          {update.isPending ? "Saving…" : "Save"}
        </Button>
      </DialogFooter>
    </form>
  );
}

const countingText = {
  chat: "exactly: renders a chat request, then tokenises it",
  text: "text only",
  none: "not at all",
} as const;

type ProvenanceProps = {
  field: EndpointField;
  provider: Provider;
  disabled: boolean;
  onHandBack: () => void;
};

/**
 * Provenance says whether the value in force is the user's or the probe's,
 * and for the user's offers to hand it back.
 */
function Provenance({ field, provider, disabled, onHandBack }: ProvenanceProps) {
  const mine = provider.overridden.includes(field);
  if (field === "pool" && !mine) {
    return <p className="text-muted-foreground text-xs">Default: the provider&apos;s name.</p>;
  }
  if (!mine) {
    return (
      <p className="text-muted-foreground text-xs">
        {provider.probed_at === undefined ? "Not probed yet." : "Found by the probe."}
      </p>
    );
  }
  return (
    <p className="flex flex-wrap items-center gap-1.5 text-xs">
      <Badge variant="secondary">Set by you</Badge>
      <button
        type="button"
        disabled={disabled}
        className="text-muted-foreground hover:text-foreground underline underline-offset-4 disabled:opacity-50"
        onClick={onHandBack}
      >
        {field === "pool" ? "Use the default" : "Use the probe's"}
      </button>
    </p>
  );
}
