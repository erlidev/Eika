/**
 * The settings of one project: the branch new workspaces use and, for a
 * remote, the credentials the hub fetches with. New credentials are tried
 * against the remote before the harness keeps them.
 */

import { useState } from "react";

import type { Project, UpdateProject } from "@/api/types";
import { Notice } from "@/components/Notice";
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
import { useUpdateProject } from "@/features/projects/queries";

export type ProjectSettingsDialogProps = {
  /** project is the project being changed; null closes the dialog. */
  project: Project | null;
  onOpenChange: (open: boolean) => void;
};

export function ProjectSettingsDialog({ project, onOpenChange }: ProjectSettingsDialogProps) {
  return (
    <Dialog
      open={project !== null}
      onOpenChange={(open) => {
        if (!open) onOpenChange(false);
      }}
    >
      <DialogContent className="sm:max-w-lg">
        {project && (
          <ProjectSettingsForm
            key={project.id}
            project={project}
            onDone={() => {
              onOpenChange(false);
            }}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

function ProjectSettingsForm({ project, onDone }: { project: Project; onDone: () => void }) {
  const update = useUpdateProject();
  const [branch, setBranch] = useState(project.default_branch);
  const [username, setUsername] = useState(project.remote_username ?? "");
  const [password, setPassword] = useState("");
  const remote = project.kind === "remote";
  const hasCredentials = project.remote_password_set === true;

  const changes: UpdateProject = {
    ...(branch.trim() !== project.default_branch ? { default_branch: branch.trim() } : {}),
    ...(remote && username.trim() !== (project.remote_username ?? "")
      ? { remote_username: username.trim() }
      : {}),
    ...(remote && password.trim() !== "" ? { remote_password: password.trim() } : {}),
  };
  const problem =
    branch.trim() === ""
      ? "The default branch cannot be empty."
      : remote && password.trim() !== "" && username.trim() === ""
        ? "A password needs a username; for a token, any username such as x-access-token works."
        : null;

  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        if (problem !== null || Object.keys(changes).length === 0) return;
        update.mutate({ id: project.id, input: changes }, { onSuccess: onDone });
      }}
    >
      <DialogHeader>
        <DialogTitle>{project.name}</DialogTitle>
        <DialogDescription className="font-mono break-all">
          {remote ? project.remote_url : project.host_path}
        </DialogDescription>
      </DialogHeader>

      <div className="space-y-1.5">
        <Label htmlFor="project-settings-branch">Default branch</Label>
        <Input
          id="project-settings-branch"
          value={branch}
          onChange={(e) => {
            setBranch(e.target.value);
          }}
        />
        <p className="text-muted-foreground text-xs">The branch a new workspace starts on.</p>
      </div>

      {remote && (
        <div className="space-y-3 rounded-lg border p-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h3 className="text-sm font-medium">Credentials</h3>
            <span className="text-muted-foreground text-xs">
              {hasCredentials
                ? `stored for ${project.remote_username ?? "a user"}`
                : "none: the remote is public"}
            </span>
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1">
              <Label htmlFor="project-settings-username" className="text-xs">
                Username
              </Label>
              <Input
                id="project-settings-username"
                value={username}
                placeholder="x-access-token"
                autoComplete="off"
                onChange={(e) => {
                  setUsername(e.target.value);
                }}
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="project-settings-password" className="text-xs">
                {hasCredentials ? "New password or token" : "Password or token"}
              </Label>
              <Input
                id="project-settings-password"
                type="password"
                value={password}
                autoComplete="off"
                placeholder={hasCredentials ? "leave blank to keep" : ""}
                onChange={(e) => {
                  setPassword(e.target.value);
                }}
              />
            </div>
          </div>
          <p className="text-muted-foreground text-xs">
            Saving new credentials fetches from the remote with them first, so a wrong token is
            refused here.
          </p>
          {hasCredentials && (
            <Button
              type="button"
              variant="outline"
              size="xs"
              disabled={update.isPending}
              onClick={() => {
                update.mutate(
                  { id: project.id, input: { remote_password: "" } },
                  { onSuccess: onDone },
                );
              }}
            >
              Remove the credentials
            </Button>
          )}
        </div>
      )}

      {problem !== null && <p className="text-muted-foreground text-xs">{problem}</p>}
      {update.isPending && remote && password !== "" && (
        <Notice tone="pending">Fetching from the remote with the new credentials…</Notice>
      )}
      {update.isError && <Notice tone="error">{update.error.message}</Notice>}

      <DialogFooter>
        <Button type="button" variant="ghost" onClick={onDone}>
          Cancel
        </Button>
        <Button
          type="submit"
          disabled={problem !== null || Object.keys(changes).length === 0 || update.isPending}
        >
          {update.isPending ? "Saving…" : "Save"}
        </Button>
      </DialogFooter>
    </form>
  );
}
