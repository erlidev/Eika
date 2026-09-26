/**
 * The form that creates and starts a sandbox for a project. Its limits,
 * network, and ports start from the settings' defaults and stay folded away
 * unless the person wants this workspace to differ; a folded section sends
 * nothing, so the harness applies the defaults it holds.
 */

import { ChevronDown, ChevronRight } from "lucide-react";
import { useState } from "react";

import type { Project, SandboxHost, SettingsState } from "@/api/types";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
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
import { Separator } from "@/components/ui/separator";
import { checkDraft, draftOf, EgressFields, LimitsFields, PortsFields } from "@/features/sandbox";
import type { SandboxDraft } from "@/features/sandbox";
import { sandboxDefaults, useSettings, useSystem } from "@/features/settings";
import { useCreateWorkspace } from "@/features/workspaces/queries";
import { failureText } from "@/lib/failure";

export type CreateWorkspaceDialogProps = {
  project: Project | null;
  onOpenChange: (open: boolean) => void;
};

export function CreateWorkspaceDialog({ project, onOpenChange }: CreateWorkspaceDialogProps) {
  const create = useCreateWorkspace();
  const settings = useSettings();
  const system = useSystem();
  const [name, setName] = useState("");
  const [branch, setBranch] = useState("");
  const [image, setImage] = useState("");
  // Null until the person opens the sandbox section: until then the
  // workspace gets whatever the defaults are when it is created.
  const [sandboxDraft, setSandboxDraft] = useState<SandboxDraft | null>(null);
  const host = system.data?.sandbox;
  const checked = sandboxDraft === null ? null : checkDraft(sandboxDraft, host);

  const problem =
    name.trim() === ""
      ? "A name is required."
      : checked !== null && checked.sandbox === undefined
        ? "Fix the sandbox fields marked above."
        : null;

  return (
    <Dialog
      open={project !== null}
      onOpenChange={(open) => {
        if (!open) onOpenChange(false);
      }}
    >
      {/* The sandbox section is long when open, so the dialog scrolls. */}
      <DialogContent className="max-h-[min(90vh,48rem)] overflow-y-auto">
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (problem || !project) return;
            create.mutate(
              {
                project_id: project.id,
                name: name.trim(),
                ...(branch.trim() === "" ? {} : { branch: branch.trim() }),
                ...(image.trim() === "" ? {} : { image: image.trim() }),
                ...(checked?.sandbox === undefined ? {} : { sandbox: checked.sandbox }),
              },
              {
                onSuccess: () => {
                  setName("");
                  setBranch("");
                  setImage("");
                  setSandboxDraft(null);
                  onOpenChange(false);
                },
              },
            );
          }}
        >
          <DialogHeader>
            <DialogTitle>New workspace</DialogTitle>
            <DialogDescription>
              A workspace is a sandbox container holding a checkout of{" "}
              {project?.name ?? "the project"}. Creating one starts it.
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-1">
            <Label htmlFor="workspace-name">Name</Label>
            <Input
              id="workspace-name"
              value={name}
              autoComplete="off"
              onChange={(e) => {
                setName(e.target.value);
              }}
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="workspace-branch">Branch</Label>
            <Input
              id="workspace-branch"
              value={branch}
              autoComplete="off"
              placeholder={project?.default_branch ?? "main"}
              onChange={(e) => {
                setBranch(e.target.value);
              }}
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="workspace-image">Image</Label>
            <Input
              id="workspace-image"
              value={image}
              autoComplete="off"
              placeholder="the sandbox image from Settings, General"
              onChange={(e) => {
                setImage(e.target.value);
              }}
            />
          </div>

          <SandboxSection
            draft={sandboxDraft}
            settings={settings.data}
            host={host}
            onChange={setSandboxDraft}
            problems={checked?.problems ?? {}}
          />

          {/* An empty name is a hint, not an error: the dialog does not open
              in red over a field nobody has typed in yet. */}
          {problem !== null ? (
            <p className="text-muted-foreground text-xs">{problem}</p>
          ) : (
            create.isError && (
              <p role="alert" className="text-destructive text-xs">
                {failureText("create the workspace", create.error)}
              </p>
            )
          )}

          <DialogFooter>
            <Button
              type="button"
              variant="secondary"
              onClick={() => {
                onOpenChange(false);
              }}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={problem !== null || create.isPending}>
              {create.isPending ? "Creating…" : "Create"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

type SandboxSectionProps = {
  /** draft is null while the section is folded and the defaults apply. */
  draft: SandboxDraft | null;
  settings: SettingsState | undefined;
  host: SandboxHost | undefined;
  onChange: (draft: SandboxDraft | null) => void;
  problems: ReturnType<typeof checkDraft>["problems"];
};

/**
 * SandboxSection folds the new workspace's limits, network, and ports away.
 * Opening it starts a draft from the defaults; folding it again drops the
 * draft, so the defaults apply after all.
 */
function SandboxSection({ draft, settings, host, onChange, problems }: SandboxSectionProps) {
  const open = draft !== null;
  const Chevron = open ? ChevronDown : ChevronRight;
  return (
    <Collapsible
      open={open}
      onOpenChange={(next) => {
        if (!next) onChange(null);
        else if (settings !== undefined) onChange(draftOf(sandboxDefaults(settings)));
      }}
      className="rounded-md border"
    >
      <CollapsibleTrigger
        disabled={settings === undefined}
        className="hover:bg-accent focus-visible:ring-ring flex w-full items-center gap-1.5 rounded-md px-2 py-1.5 text-left text-sm transition-colors focus-visible:ring-1 focus-visible:outline-none"
      >
        <Chevron aria-hidden className="text-muted-foreground size-4 shrink-0" />
        <span className="font-medium">Sandbox</span>
        <span className="text-muted-foreground truncate text-xs">
          {open ? "this workspace's own" : "the defaults from Settings, Sandbox"}
        </span>
      </CollapsibleTrigger>
      <CollapsibleContent className="space-y-4 border-t p-3">
        {draft !== null && (
          <>
            <LimitsFields
              id="new-workspace"
              draft={draft}
              onChange={(change) => {
                onChange({ ...draft, ...change });
              }}
              host={host}
              problems={problems}
            />
            <Separator />
            <EgressFields
              id="new-workspace"
              draft={draft}
              onChange={(change) => {
                onChange({ ...draft, ...change });
              }}
              host={host}
              problems={problems}
            />
            <Separator />
            <PortsFields
              id="new-workspace"
              draft={draft}
              onChange={(change) => {
                onChange({ ...draft, ...change });
              }}
              host={host}
              problems={problems}
            />
          </>
        )}
      </CollapsibleContent>
    </Collapsible>
  );
}
