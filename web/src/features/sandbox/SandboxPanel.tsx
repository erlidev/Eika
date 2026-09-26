/**
 * The Sandbox panel: what a workspace's container consumes against its
 * limits, sampled every few seconds while it runs; the hosts its egress was
 * refused lately, each a click from the allowlist; its forwarded ports, each
 * opening a preview; and the editor of its limits, network, and ports, which
 * apply at once without a restart.
 */

import { ArrowDown, ArrowUp, ExternalLink } from "lucide-react";
import { useState } from "react";

import type { Sandbox, SandboxHost, Workspace, WorkspaceUsage } from "@/api/types";
import { ActionError, LoadError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { checkDraft, draftOf, formatCores, sameSandbox } from "@/features/sandbox/form";
import type { SandboxDraft } from "@/features/sandbox/form";
import { useOpenPreview, useSetSandbox, useWorkspaceUsage } from "@/features/sandbox/queries";
import { EgressFields, LimitsFields, PortsFields } from "@/features/sandbox/SandboxFields";
import { useSystem } from "@/features/settings";
import { useWorkspace, useWorkspaceEvents } from "@/features/workspaces";
import { formatAgo, formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";

export type SandboxPanelProps = {
  workspaceId: string;
};

export function SandboxPanel({ workspaceId }: SandboxPanelProps) {
  useWorkspaceEvents(workspaceId);
  const workspace = useWorkspace(workspaceId);
  const system = useSystem();

  if (workspace.isPending) {
    return (
      <div className="p-3">
        <Notice tone="pending">Loading the workspace…</Notice>
      </div>
    );
  }
  if (workspace.isError) {
    return (
      <div className="p-3">
        <LoadError
          what="the workspace"
          error={workspace.error}
          retrying={workspace.isFetching}
          retry={() => void workspace.refetch()}
        />
      </div>
    );
  }
  const ws = workspace.data;
  const running = ws.state === "running";
  return (
    <div className="space-y-4 p-3">
      {running ? (
        <UsageSection workspace={ws} />
      ) : (
        <Notice>
          The workspace is {ws.state}. Its usage shows while it runs; its limits and network can be
          changed now and hold when it starts.
        </Notice>
      )}
      <PreviewSection workspace={ws} running={running} />
      <Separator />
      {/* Keyed by what is stored, so the editor starts from it again after a save
          or a change made elsewhere, and keeps unsaved edits otherwise. */}
      <SandboxEditor key={JSON.stringify(ws.sandbox)} workspace={ws} host={system.data?.sandbox} />
    </div>
  );
}

function UsageSection({ workspace }: { workspace: Workspace }) {
  const usage = useWorkspaceUsage(workspace.id, true);
  if (usage.isPending) return <Notice tone="pending">Sampling the workspace…</Notice>;
  if (usage.isError) {
    return (
      <LoadError
        what="the workspace's usage"
        error={usage.error}
        retrying={usage.isFetching}
        retry={() => void usage.refetch()}
      />
    );
  }
  return (
    <div className="space-y-3">
      <section aria-labelledby={`usage-${workspace.id}`} className="space-y-2">
        <div className="flex items-baseline gap-2">
          <h3 id={`usage-${workspace.id}`} className="text-sm font-medium">
            Usage
          </h3>
          <span className="text-muted-foreground text-xs">sampled every 5 seconds</span>
        </div>
        <UsageMeters usage={usage.data} />
      </section>
      <BlockedHosts workspace={workspace} usage={usage.data} />
    </div>
  );
}

/** UsageMeters draws one bar per resource against its limit, or the host's whole. */
function UsageMeters({ usage }: { usage: WorkspaceUsage }) {
  const cores = usage.cpus > 0 ? usage.cpus : 1;
  return (
    <dl className="grid grid-cols-[auto_1fr] items-center gap-x-3 gap-y-1.5 text-xs">
      <Meter
        label="CPU"
        fraction={usage.cpu_percent / (cores * 100)}
        text={`${String(Math.round(usage.cpu_percent))}% of ${formatCores(usage.cpus)}`}
      />
      <Meter
        label="Memory"
        fraction={usage.memory_limit_bytes > 0 ? usage.memory_bytes / usage.memory_limit_bytes : 0}
        text={`${formatBytes(usage.memory_bytes)} of ${formatBytes(usage.memory_limit_bytes)}`}
      />
      <Meter
        label="Processes"
        fraction={usage.pids_limit > 0 ? usage.pids / usage.pids_limit : undefined}
        text={
          usage.pids_limit > 0
            ? `${String(usage.pids)} of ${usage.pids_limit.toLocaleString("en-US")}`
            : `${String(usage.pids)}, no limit`
        }
      />
      <dt className="text-muted-foreground">Network</dt>
      <dd className="flex items-center gap-3 font-mono tabular-nums">
        <span className="inline-flex items-center gap-1">
          <ArrowDown aria-label="received" className="text-muted-foreground size-3.5" />
          {formatBytes(usage.network_rx_bytes)}
        </span>
        <span className="inline-flex items-center gap-1">
          <ArrowUp aria-label="sent" className="text-muted-foreground size-3.5" />
          {formatBytes(usage.network_tx_bytes)}
        </span>
      </dd>
    </dl>
  );
}

type MeterProps = {
  label: string;
  /** fraction is how much of the limit is used; undefined draws no bar. */
  fraction: number | undefined;
  text: string;
};

function Meter({ label, fraction, text }: MeterProps) {
  const clamped = fraction === undefined ? undefined : Math.min(Math.max(fraction, 0), 1);
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="flex min-w-0 items-center gap-2">
        {clamped !== undefined && (
          <div
            role="meter"
            aria-label={`${label} used`}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={Math.round(clamped * 100)}
            className="bg-muted h-1.5 w-24 shrink-0 overflow-hidden rounded-full"
          >
            <div
              className={cn(
                "h-full rounded-full",
                clamped > 0.95 ? "bg-destructive" : clamped > 0.8 ? "bg-warning" : "bg-primary",
              )}
              style={{ width: `${String(clamped * 100)}%` }}
            />
          </div>
        )}
        <span className="truncate font-mono tabular-nums">{text}</span>
      </dd>
    </>
  );
}

/**
 * BlockedHosts lists the hosts the egress proxy refused the workspace
 * lately. Under an allowlist, each can be added to it at once.
 */
function BlockedHosts({ workspace, usage }: { workspace: Workspace; usage: WorkspaceUsage }) {
  const save = useSetSandbox();
  const blocked = usage.blocked ?? [];
  const mode = workspace.sandbox.egress.mode;
  if (mode === "open" || blocked.length === 0) return null;
  const allow = (host: string) => {
    const current = workspace.sandbox.egress.allow ?? [];
    save.mutate({
      workspaceId: workspace.id,
      sandbox: {
        ...workspace.sandbox,
        egress: { ...workspace.sandbox.egress, allow: [...current, host] },
      },
    });
  };
  return (
    <section aria-labelledby={`blocked-${workspace.id}`} className="space-y-1.5">
      <h3 id={`blocked-${workspace.id}`} className="text-sm font-medium">
        Refused lately
      </h3>
      <ul className="divide-y rounded-md border">
        {blocked.map((b) => (
          <li key={b.host} className="flex min-h-8 items-center gap-2 px-2 py-1 text-xs">
            <span className="min-w-0 flex-1 truncate font-mono">{b.host}</span>
            <span className="text-muted-foreground shrink-0 tabular-nums">
              {b.count === 1 ? "once" : `${String(b.count)} times`}, {formatAgo(b.last)}
            </span>
            {mode === "allowlist" && (
              <Button
                type="button"
                size="xs"
                variant="outline"
                disabled={save.isPending}
                onClick={() => {
                  allow(b.host);
                }}
              >
                Allow<span className="sr-only"> {b.host}</span>
              </Button>
            )}
          </li>
        ))}
      </ul>
      {save.isError && <ActionError action="allow the host" error={save.error} />}
    </section>
  );
}

/** PreviewSection lists the stored forwarded ports, each opening a preview in a new tab. */
function PreviewSection({ workspace, running }: { workspace: Workspace; running: boolean }) {
  const open = useOpenPreview();
  const ports = workspace.sandbox.ports ?? [];
  if (ports.length === 0) return null;
  return (
    <section aria-labelledby={`previews-${workspace.id}`} className="space-y-1.5">
      <h3 id={`previews-${workspace.id}`} className="text-sm font-medium">
        Previews
      </h3>
      <ul className="divide-y rounded-md border">
        {ports.map((p) => (
          <li key={p.port} className="flex min-h-8 items-center gap-2 px-2 py-1 text-xs">
            <span className="font-mono">{p.port}</span>
            <span className="text-muted-foreground min-w-0 flex-1 truncate">{p.label}</span>
            <Button
              type="button"
              size="xs"
              variant="outline"
              disabled={!running || open.isPending}
              onClick={() => {
                open.mutate({ workspaceId: workspace.id, port: p.port });
              }}
            >
              <ExternalLink aria-hidden />
              Open<span className="sr-only"> port {p.port}</span>
            </Button>
          </li>
        ))}
      </ul>
      {open.isError && <ActionError action="open the preview" error={open.error} />}
    </section>
  );
}

type SandboxEditorProps = {
  workspace: Workspace;
  host: SandboxHost | undefined;
};

/** SandboxEditor edits the workspace's limits, network, and ports, and applies them on save. */
function SandboxEditor({ workspace, host }: SandboxEditorProps) {
  const save = useSetSandbox();
  const [draft, setDraft] = useState<SandboxDraft>(() => draftOf(workspace.sandbox));
  const { sandbox, problems } = checkDraft(draft, host);
  const changed = sandbox === undefined || !sameSandbox(sandbox, workspace.sandbox);
  const change = (next: Partial<SandboxDraft>) => {
    setDraft((d) => ({ ...d, ...next }));
  };
  const id = `sandbox-${workspace.id}`;
  return (
    <form
      className="space-y-4"
      aria-label="Sandbox"
      onSubmit={(e) => {
        e.preventDefault();
        if (sandbox === undefined || !changed) return;
        save.mutate({ workspaceId: workspace.id, sandbox: sandbox satisfies Sandbox });
      }}
    >
      <PortsFields id={id} draft={draft} onChange={change} host={host} problems={problems} />
      <Separator />
      <section aria-label="Limits" className="space-y-2">
        <h3 className="text-sm font-medium">Limits</h3>
        <LimitsFields id={id} draft={draft} onChange={change} host={host} problems={problems} />
      </section>
      <Separator />
      <EgressFields id={id} draft={draft} onChange={change} host={host} problems={problems} />
      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="submit"
          size="sm"
          disabled={!changed || sandbox === undefined || save.isPending}
        >
          {save.isPending ? "Applying…" : "Apply"}
        </Button>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          disabled={!changed || save.isPending}
          onClick={() => {
            setDraft(draftOf(workspace.sandbox));
          }}
        >
          Revert
        </Button>
        <p className="text-muted-foreground min-w-40 flex-1 text-xs">
          Applies at once, without a restart. A change of network drops the connections open at that
          moment.
        </p>
      </div>
      {save.isError && <ActionError action="apply the sandbox" error={save.error} />}
    </form>
  );
}
