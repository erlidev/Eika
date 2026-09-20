/**
 * The Files panel: the workspace's tree beside (or, in a narrow pane, above)
 * an editor for the file picked in it. Saving writes through the harness,
 * which refreshes the Changes panel's diff too.
 */

import { Save } from "lucide-react";
import { lazy, Suspense } from "react";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import { ActionError, LoadError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import {
  afterSave,
  decideOpen,
  isDirty,
  toggle,
  useWorkspaceFiles,
} from "@/features/files/editing";
import { FileTree } from "@/features/files/FileTree";
import { useFileContent, useSaveFile } from "@/features/files/queries";
import { RunningWorkspace } from "@/features/workspaces";
import { formatBytes } from "@/lib/format";

const CodeEditor = lazy(() =>
  import("@/features/files/CodeEditor").then((module) => ({ default: module.CodeEditor })),
);

export type FilesPanelProps = {
  workspaceId: string;
};

export function FilesPanel({ workspaceId }: FilesPanelProps) {
  return (
    <RunningWorkspace workspaceId={workspaceId} purpose="browse its files">
      <FilesView workspaceId={workspaceId} />
    </RunningWorkspace>
  );
}

function FilesView({ workspaceId }: { workspaceId: string }) {
  const [files, update] = useWorkspaceFiles(workspaceId);
  const { open, pending, expanded } = files;
  const content = useFileContent(workspaceId, open?.path);
  const save = useSaveFile(workspaceId);

  const saved = content.data?.content;
  const dirty = isDirty(open, saved);

  const requestOpen = (path: string) => {
    const decision = decideOpen(open, saved, path);
    if (decision === "open") update((f) => ({ ...f, open: { path } }));
    else if (decision === "confirm") update((f) => ({ ...f, pending: path }));
  };

  const saveOpen = () => {
    if (open?.draft === undefined || !dirty || save.isPending) return;
    const { path, draft } = open;
    save.mutate(
      { path, content: draft },
      {
        onSuccess: () => {
          update((f) => ({ ...f, open: afterSave(f.open, path, draft) }));
        },
      },
    );
  };

  return (
    <div
      className="@container flex h-full min-h-0 flex-col"
      onKeyDown={(e) => {
        if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
          e.preventDefault();
          saveOpen();
        }
      }}
    >
      <div className="flex min-h-0 flex-1 flex-col @2xl:flex-row">
        <div className="max-h-2/5 shrink-0 overflow-auto border-b @2xl:max-h-none @2xl:w-56 @2xl:border-r @2xl:border-b-0">
          <FileTree
            workspaceId={workspaceId}
            expanded={expanded}
            selected={open?.path}
            onToggle={(path) => {
              update((f) => ({ ...f, expanded: toggle(f.expanded, path) }));
            }}
            onOpen={requestOpen}
          />
        </div>
        <section aria-label="Editor" className="flex min-h-0 min-w-0 flex-1 flex-col">
          {open === undefined ? (
            <p className="text-muted-foreground p-3 text-xs">Pick a file to open it.</p>
          ) : (
            <>
              <div className="flex h-8 shrink-0 items-center gap-2 border-b px-2">
                <span className="min-w-0 truncate font-mono text-xs" title={open.path}>
                  {open.path}
                </span>
                {dirty && (
                  <span
                    role="img"
                    aria-label="Unsaved changes"
                    title="Unsaved changes"
                    className="bg-warning size-1.5 shrink-0 rounded-full"
                  />
                )}
                <Button
                  type="button"
                  size="xs"
                  variant={dirty ? "default" : "outline"}
                  className="ml-auto"
                  disabled={!dirty || save.isPending}
                  onClick={saveOpen}
                >
                  <Save aria-hidden className="size-3" />
                  {save.isPending ? "Saving…" : "Save"}
                </Button>
              </div>
              {save.isError && (
                <ActionError className="m-2" action={`save ${open.path}`} error={save.error} />
              )}
              <div className="min-h-0 flex-1">
                <FileBody
                  path={open.path}
                  content={content}
                  draft={open.draft}
                  onChange={(text) => {
                    update((f) =>
                      f.open?.path === open.path ? { ...f, open: { ...f.open, draft: text } } : f,
                    );
                  }}
                  onSave={saveOpen}
                />
              </div>
            </>
          )}
        </section>
      </div>
      <ConfirmDialog
        open={pending !== undefined}
        onOpenChange={(show) => {
          if (!show) update((f) => ({ ...f, pending: undefined }));
        }}
        title="Discard unsaved changes?"
        description={`${open?.path ?? "The open file"} has changes that are not saved. Opening ${pending ?? "another file"} discards them.`}
        confirmLabel="Discard"
        onConfirm={() => {
          if (pending !== undefined)
            update((f) => ({ ...f, open: { path: pending }, pending: undefined }));
        }}
      />
    </div>
  );
}

type FileBodyProps = {
  path: string;
  content: ReturnType<typeof useFileContent>;
  draft: string | undefined;
  onChange: (text: string) => void;
  onSave: () => void;
};

function FileBody({ path, content, draft, onChange, onSave }: FileBodyProps) {
  if (content.isPending) {
    return (
      <div className="p-3">
        <Notice tone="pending">Loading {path}…</Notice>
      </div>
    );
  }
  if (content.isError) {
    return (
      <div className="p-3">
        <LoadError
          what={path}
          error={content.error}
          retrying={content.isFetching}
          retry={() => void content.refetch()}
        />
      </div>
    );
  }
  const file = content.data;
  if (file.binary || file.too_large) {
    return (
      <div className="p-3">
        <Notice>
          {file.binary
            ? `${path} is a binary file (${formatBytes(file.size)}), which the editor does not open.`
            : `${path} is ${formatBytes(file.size)}, over the 2 MiB the editor opens.`}
        </Notice>
      </div>
    );
  }
  return (
    <Suspense
      fallback={
        <div className="p-3">
          <Notice tone="pending">Loading the editor…</Notice>
        </div>
      }
    >
      <CodeEditor path={path} value={draft ?? file.content} onChange={onChange} onSave={onSave} />
    </Suspense>
  );
}
