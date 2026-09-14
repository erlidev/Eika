/** The form that registers a repository with Eika. */

import { useState } from "react";

import type { CreateProject, ProjectKind } from "@/api/types";
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
import { useCreateProject } from "@/features/projects/queries";
import { validate } from "@/features/projects/validate";

export type CreateProjectDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
};

export function CreateProjectDialog({ open, onOpenChange }: CreateProjectDialogProps) {
  const create = useCreateProject();
  const [name, setName] = useState("");
  const [kind, setKind] = useState<ProjectKind>("local");
  const [remoteUrl, setRemoteUrl] = useState("");
  const [usernameEnv, setUsernameEnv] = useState("");
  const [passwordEnv, setPasswordEnv] = useState("");
  const [hostPath, setHostPath] = useState("");
  const [defaultBranch, setDefaultBranch] = useState("main");

  const input: CreateProject = {
    name: name.trim(),
    kind,
    default_branch: defaultBranch.trim() || "main",
    ...(kind === "remote"
      ? {
          remote_url: remoteUrl.trim(),
          remote_username_env: usernameEnv.trim(),
          remote_password_env: passwordEnv.trim(),
        }
      : { host_path: hostPath.trim() }),
  };
  const problem = validate(input);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (problem) return;
            create.mutate(input, {
              onSuccess: () => {
                setName("");
                setRemoteUrl("");
                setUsernameEnv("");
                setPasswordEnv("");
                setHostPath("");
                onOpenChange(false);
              },
            });
          }}
        >
          <DialogHeader>
            <DialogTitle>New project</DialogTitle>
            <DialogDescription>
              A project is a git repository Eika mirrors in its hub. Workspaces check it out.
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-1">
            <Label htmlFor="project-name">Name</Label>
            <Input
              id="project-name"
              value={name}
              autoComplete="off"
              onChange={(e) => {
                setName(e.target.value);
              }}
            />
          </div>

          <fieldset className="space-y-1">
            <legend className="text-sm font-medium">Kind</legend>
            <div className="flex gap-4 text-sm">
              {(["local", "remote"] as const).map((option) => (
                <label key={option} className="flex items-center gap-2">
                  <input
                    type="radio"
                    name="project-kind"
                    value={option}
                    checked={kind === option}
                    onChange={() => {
                      setKind(option);
                    }}
                  />
                  {option === "local" ? "Local directory" : "Remote repository"}
                </label>
              ))}
            </div>
          </fieldset>

          {kind === "remote" ? (
            <div className="space-y-1">
              <Label htmlFor="project-remote">Remote URL</Label>
              <Input
                id="project-remote"
                value={remoteUrl}
                placeholder="https://github.com/owner/repo.git"
                autoComplete="off"
                onChange={(e) => {
                  setRemoteUrl(e.target.value);
                }}
              />
              <p className="text-muted-foreground text-xs">
                No username, password, query string, or fragment. A private remote names the harness
                environment variables that hold its credentials below.
              </p>
              <div className="grid grid-cols-2 gap-2 pt-1">
                <div className="space-y-1">
                  <Label htmlFor="project-user-env">Username variable</Label>
                  <Input
                    id="project-user-env"
                    value={usernameEnv}
                    placeholder="EIKA_GIT_USERNAME"
                    autoComplete="off"
                    onChange={(e) => {
                      setUsernameEnv(e.target.value);
                    }}
                  />
                </div>
                <div className="space-y-1">
                  <Label htmlFor="project-password-env">Password variable</Label>
                  <Input
                    id="project-password-env"
                    value={passwordEnv}
                    placeholder="EIKA_GIT_PASSWORD"
                    autoComplete="off"
                    onChange={(e) => {
                      setPasswordEnv(e.target.value);
                    }}
                  />
                </div>
              </div>
            </div>
          ) : (
            <div className="space-y-1">
              <Label htmlFor="project-path">Host path</Label>
              <Input
                id="project-path"
                value={hostPath}
                placeholder="/home/you/code/project"
                autoComplete="off"
                onChange={(e) => {
                  setHostPath(e.target.value);
                }}
              />
              <p className="text-muted-foreground text-xs">
                An absolute path on the Docker host, bind-mounted at the workspace root.
              </p>
            </div>
          )}

          <div className="space-y-1">
            <Label htmlFor="project-branch">Default branch</Label>
            <Input
              id="project-branch"
              value={defaultBranch}
              autoComplete="off"
              onChange={(e) => {
                setDefaultBranch(e.target.value);
              }}
            />
          </div>

          {(problem ?? create.isError) && (
            <p role="alert" className="text-destructive text-xs">
              {problem ?? create.error?.message}
            </p>
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
              Create
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
