/**
 * The Changes panel: what the workspace changed against the commit it started
 * from, file by file, and the actions that take the work somewhere: commit,
 * push to the hub, and push upstream with a link to open a pull request.
 */

import { ChevronDown, ChevronRight, ExternalLink, RefreshCw } from "lucide-react";
import { useState } from "react";

import { ApiError } from "@/api/client";
import type { PushResult } from "@/api/types";
import { DiffRows } from "@/components/DiffRows";
import { ActionError, LoadError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { useCommit, usePush, useWorkspaceDiff } from "@/features/changes/queries";
import { useProjects } from "@/features/projects";
import { RunningWorkspace, useWorkspace } from "@/features/workspaces";
import { compareUrl } from "@/lib/github";
import { parseStatus, parseUnifiedDiff, summarizeStatus } from "@/lib/unifiedDiff";
import type { FileChange, FileDiff } from "@/lib/unifiedDiff";
import { shortId } from "@/lib/format";
import { cn } from "@/lib/utils";

export type ChangesPanelProps = {
  workspaceId: string;
};

export function ChangesPanel({ workspaceId }: ChangesPanelProps) {
  return (
    <RunningWorkspace workspaceId={workspaceId} purpose="see its changes">
      <ChangesView workspaceId={workspaceId} />
    </RunningWorkspace>
  );
}

function ChangesView({ workspaceId }: { workspaceId: string }) {
  const diff = useWorkspaceDiff(workspaceId);

  if (diff.isPending) {
    return (
      <div className="p-3">
        <Notice tone="pending">Loading the changes…</Notice>
      </div>
    );
  }
  if (diff.isError) {
    return (
      <div className="p-3">
        <LoadError
          what="the changes"
          error={diff.error}
          retrying={diff.isFetching}
          retry={() => void diff.refetch()}
        />
      </div>
    );
  }

  const files = parseUnifiedDiff(diff.data.diff);
  const status = parseStatus(diff.data.status);
  const summary = summarizeStatus(status);
  const inDiff = new Set(files.map((f) => f.path));
  const untracked = status.filter((e) => e.code === "??" && !inDiff.has(e.path));

  return (
    <div className="space-y-4 p-3 text-xs">
      <div className="flex items-center gap-2">
        <p role="status" className="text-muted-foreground min-w-0 flex-1 truncate">
          {describeSummary(summary)}
        </p>
        <Button
          type="button"
          size="icon-xs"
          variant="ghost"
          aria-label="Refresh the changes"
          title="Refresh the changes"
          disabled={diff.isFetching}
          onClick={() => void diff.refetch()}
        >
          <RefreshCw aria-hidden className={cn("size-3", diff.isFetching && "animate-spin")} />
        </Button>
      </div>

      <CommitForm workspaceId={workspaceId} />
      <PushActions workspaceId={workspaceId} />

      <section aria-label="Changed files" className="space-y-1">
        {files.length === 0 && untracked.length === 0 && (
          <p className="text-muted-foreground">No changes against the base commit.</p>
        )}
        {files.map((file) => (
          <FileChanges key={`${file.oldPath}:${file.path}`} file={file} />
        ))}
        {untracked.map((entry) => (
          <div
            key={entry.path}
            className="flex items-center gap-1.5 rounded-md border px-2 py-1 font-mono"
          >
            <ChangeBadge change="untracked" />
            <span className="min-w-0 flex-1 truncate" title={entry.path}>
              {entry.path}
            </span>
          </div>
        ))}
      </section>
    </div>
  );
}

function describeSummary(summary: ReturnType<typeof summarizeStatus>): string {
  const parts: string[] = [];
  if (summary.changed > 0) parts.push(`${String(summary.changed)} changed`);
  if (summary.untracked > 0) parts.push(`${String(summary.untracked)} untracked`);
  if (summary.conflicted > 0) parts.push(`${String(summary.conflicted)} conflicted`);
  return parts.length === 0 ? "Nothing uncommitted." : `Uncommitted: ${parts.join(", ")}.`;
}

const badges: Record<FileChange | "untracked", { letter: string; label: string; tone: string }> = {
  modified: { letter: "M", label: "modified", tone: "text-warning" },
  added: { letter: "A", label: "added", tone: "text-success" },
  deleted: { letter: "D", label: "deleted", tone: "text-destructive" },
  renamed: { letter: "R", label: "renamed", tone: "text-primary" },
  copied: { letter: "C", label: "copied", tone: "text-primary" },
  untracked: { letter: "U", label: "untracked", tone: "text-success" },
};

function ChangeBadge({ change }: { change: FileChange | "untracked" }) {
  const badge = badges[change];
  return (
    <span
      title={badge.label}
      aria-label={badge.label}
      className={cn("w-3 shrink-0 text-center font-semibold", badge.tone)}
    >
      {badge.letter}
    </span>
  );
}

function FileChanges({ file }: { file: FileDiff }) {
  const [open, setOpen] = useState(true);
  const Chevron = open ? ChevronDown : ChevronRight;
  const name = file.oldPath !== file.path ? `${file.oldPath} -> ${file.path}` : file.path;
  return (
    <Collapsible open={open} onOpenChange={setOpen} className="rounded-md border">
      <CollapsibleTrigger className="hover:bg-accent focus-visible:ring-ring flex w-full items-center gap-1.5 px-2 py-1 text-left font-mono transition-colors focus-visible:ring-1 focus-visible:outline-none">
        <Chevron aria-hidden className="text-muted-foreground size-3.5 shrink-0" />
        <ChangeBadge change={file.change} />
        <span className="min-w-0 flex-1 truncate" title={name}>
          {name}
        </span>
        {!file.binary && (
          <span className="shrink-0 tabular-nums">
            <span className="text-success">+{file.additions}</span>{" "}
            <span className="text-destructive">-{file.deletions}</span>
          </span>
        )}
      </CollapsibleTrigger>
      <CollapsibleContent className="border-t">
        {file.binary ? (
          <p className="text-muted-foreground px-2 py-1">Binary file; there is no text diff.</p>
        ) : file.hunks.length === 0 ? (
          <p className="text-muted-foreground px-2 py-1">Only the name or mode changed.</p>
        ) : (
          <div
            tabIndex={0}
            role="group"
            aria-label={`Diff of ${file.path}`}
            className="focus-visible:ring-ring overflow-x-auto font-mono leading-relaxed focus-visible:ring-1 focus-visible:outline-none"
          >
            {file.hunks.map((hunk, index) => (
              <div key={`${String(hunk.oldStart)}:${String(index)}`}>
                <div className="bg-muted/50 text-muted-foreground truncate px-2 select-none">
                  @@ -{hunk.oldStart} +{hunk.newStart} @@ {hunk.header ?? ""}
                </div>
                <DiffRows
                  lines={hunk.lines}
                  lineNumber={(line) => (line.kind === "remove" ? line.oldLine : line.newLine)}
                />
              </div>
            ))}
          </div>
        )}
      </CollapsibleContent>
    </Collapsible>
  );
}

function CommitForm({ workspaceId }: { workspaceId: string }) {
  const commit = useCommit(workspaceId);
  const [message, setMessage] = useState("");
  // A stopped workspace is a 409 too, so the message tells them apart.
  const nothingToCommit =
    commit.error instanceof ApiError &&
    commit.error.status === 409 &&
    /nothing to commit/i.test(commit.error.message);

  return (
    <form
      className="space-y-2"
      onSubmit={(e) => {
        e.preventDefault();
        if (message.trim() === "") return;
        commit.mutate(
          { message: message.trim() },
          {
            onSuccess: () => {
              setMessage("");
            },
          },
        );
      }}
    >
      <Label htmlFor="commit-message" className="text-xs">
        Commit message
      </Label>
      <Textarea
        id="commit-message"
        value={message}
        rows={2}
        placeholder="Describe the change…"
        className="min-h-12 text-sm"
        onChange={(e) => {
          setMessage(e.target.value);
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
            e.preventDefault();
            e.currentTarget.form?.requestSubmit();
          }
        }}
      />
      <div className="flex items-center gap-2">
        <Button type="submit" size="sm" disabled={message.trim() === "" || commit.isPending}>
          {commit.isPending ? "Committing…" : "Commit all"}
        </Button>
      </div>
      {commit.isSuccess && (
        <Notice tone="success">
          Committed <span className="font-mono">{shortId(commit.data.commit)}</span>.
        </Notice>
      )}
      {nothingToCommit && (
        <Notice>Nothing to commit: the workspace has no uncommitted changes.</Notice>
      )}
      {commit.isError && !nothingToCommit && <ActionError action="commit" error={commit.error} />}
    </form>
  );
}

function PushActions({ workspaceId }: { workspaceId: string }) {
  const push = usePush(workspaceId);
  const workspace = useWorkspace(workspaceId);
  const projects = useProjects();
  const project = projects.data?.find((p) => p.id === workspace.data?.project_id);
  const upstream = push.variables?.upstream === true;
  // The harness refuses an upstream push for a project with no remote.
  const hasRemote = (project?.remote_url ?? "") !== "";

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={push.isPending}
          onClick={() => {
            push.mutate({});
          }}
        >
          {push.isPending && !upstream ? "Pushing…" : "Push"}
        </Button>
        {hasRemote && (
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={push.isPending}
            onClick={() => {
              push.mutate({ upstream: true });
            }}
          >
            {push.isPending && upstream ? "Pushing upstream…" : "Push upstream"}
          </Button>
        )}
      </div>
      {push.isSuccess && <PushNotice result={push.data} remoteUrl={project?.remote_url} />}
      {push.isError && (
        <ActionError action={upstream ? "push upstream" : "push to the hub"} error={push.error} />
      )}
    </div>
  );
}

function PushNotice({ result, remoteUrl }: { result: PushResult; remoteUrl: string | undefined }) {
  const link = result.upstream_pushed ? compareUrl(remoteUrl, result.branch) : undefined;
  return (
    <Notice tone="success">
      Pushed <span className="font-mono">{result.branch}</span> at{" "}
      <span className="font-mono">{shortId(result.commit)}</span>{" "}
      {result.upstream_pushed ? "to the hub and upstream." : "to the hub."}
      {link !== undefined && (
        <a
          href={link}
          target="_blank"
          rel="noreferrer"
          className="text-primary mt-1 flex items-center gap-1 font-medium hover:underline"
        >
          Open a pull request on GitHub
          <ExternalLink aria-hidden className="size-3.5" />
        </a>
      )}
    </Notice>
  );
}
