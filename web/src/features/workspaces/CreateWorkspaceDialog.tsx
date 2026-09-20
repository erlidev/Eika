/** The form that creates and starts a sandbox for a project. */

import { useState } from "react";

import type { Project } from "@/api/types";
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
import { useCreateWorkspace } from "@/features/workspaces/queries";
import { failureText } from "@/lib/failure";

export type CreateWorkspaceDialogProps = {
  project: Project | null;
  onOpenChange: (open: boolean) => void;
};

export function CreateWorkspaceDialog({ project, onOpenChange }: CreateWorkspaceDialogProps) {
  const create = useCreateWorkspace();
  const [name, setName] = useState("");
  const [branch, setBranch] = useState("");
  const [image, setImage] = useState("");

  const problem = name.trim() === "" ? "A name is required." : null;

  return (
    <Dialog
      open={project !== null}
      onOpenChange={(open) => {
        if (!open) onOpenChange(false);
      }}
    >
      <DialogContent>
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
              },
              {
                onSuccess: () => {
                  setName("");
                  setBranch("");
                  setImage("");
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
