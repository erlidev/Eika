/**
 * The form that registers a repository with Eika: a directory on the Docker
 * host, or a remote the hub mirrors, with the credentials a private remote
 * needs. The create dialog and the setup wizard both use it.
 */

import { useState } from "react";

import type { CreateProject, Project, ProjectKind } from "@/api/types";
import { ActionError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useCreateProject } from "@/features/projects/queries";
import { validate } from "@/features/projects/validate";
import { cn } from "@/lib/utils";

export type ProjectFormProps = {
  onCreated: (project: Project) => void;
  /** onCancel shows a secondary button when set. */
  onCancel?: () => void;
  cancelLabel?: string;
  submitLabel?: string;
};

const kinds: readonly { value: ProjectKind; label: string; blurb: string }[] = [
  { value: "local", label: "Local directory", blurb: "A repository on this machine" },
  { value: "remote", label: "Remote repository", blurb: "GitHub, GitLab, or any git URL" },
];

export function ProjectForm({ onCreated, onCancel, cancelLabel, submitLabel }: ProjectFormProps) {
  const create = useCreateProject();
  const [name, setName] = useState("");
  const [kind, setKind] = useState<ProjectKind>("local");
  const [remoteUrl, setRemoteUrl] = useState("");
  const [isPrivate, setIsPrivate] = useState(false);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [hostPath, setHostPath] = useState("");
  const [defaultBranch, setDefaultBranch] = useState("main");

  const input: CreateProject = {
    name: name.trim(),
    kind,
    default_branch: defaultBranch.trim() || "main",
    ...(kind === "remote"
      ? {
          remote_url: remoteUrl.trim(),
          ...(isPrivate
            ? { remote_username: username.trim(), remote_password: password.trim() }
            : {}),
        }
      : { host_path: hostPath.trim() }),
  };
  const problem = validate(input);
  const touched = name !== "" || remoteUrl !== "" || hostPath !== "";

  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        if (problem) return;
        create.mutate(input, { onSuccess: onCreated });
      }}
    >
      <fieldset className="space-y-1.5">
        <legend className="text-sm font-medium">Where the code is</legend>
        <div className="grid grid-cols-2 gap-2">
          {kinds.map((option) => (
            <button
              key={option.value}
              type="button"
              aria-pressed={kind === option.value}
              onClick={() => {
                setKind(option.value);
              }}
              className={cn(
                "hover:bg-muted/60 focus-visible:ring-ring/50 rounded-md border px-3 py-2 text-left outline-none focus-visible:ring-3",
                kind === option.value && "border-primary bg-muted/60 ring-primary/20 ring-2",
              )}
            >
              <span className="block text-sm font-medium">{option.label}</span>
              <span className="text-muted-foreground block text-xs">{option.blurb}</span>
            </button>
          ))}
        </div>
      </fieldset>

      <div className="grid gap-4 sm:grid-cols-[2fr_1fr]">
        <div className="space-y-1.5">
          <Label htmlFor="project-name">Name</Label>
          <Input
            id="project-name"
            value={name}
            autoComplete="off"
            placeholder="my-app"
            onChange={(e) => {
              setName(e.target.value);
            }}
          />
        </div>
        <div className="space-y-1.5">
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
      </div>

      {kind === "remote" ? (
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="project-remote">Remote URL</Label>
            <Input
              id="project-remote"
              value={remoteUrl}
              placeholder="https://github.com/owner/repo.git"
              autoComplete="off"
              className="font-mono"
              onChange={(e) => {
                setRemoteUrl(e.target.value);
              }}
            />
          </div>
          <label className="flex items-center gap-2 text-sm">
            <Checkbox
              checked={isPrivate}
              onCheckedChange={(value) => {
                setIsPrivate(value === true);
              }}
            />
            The repository is private
          </label>
          {isPrivate && (
            <div className="bg-muted/30 space-y-3 rounded-md border p-3">
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-1">
                  <Label htmlFor="project-username" className="text-xs">
                    Username
                  </Label>
                  <Input
                    id="project-username"
                    value={username}
                    placeholder="x-access-token"
                    autoComplete="off"
                    onChange={(e) => {
                      setUsername(e.target.value);
                    }}
                  />
                </div>
                <div className="space-y-1">
                  <Label htmlFor="project-password" className="text-xs">
                    Password or access token
                  </Label>
                  <Input
                    id="project-password"
                    type="password"
                    value={password}
                    autoComplete="off"
                    onChange={(e) => {
                      setPassword(e.target.value);
                    }}
                  />
                </div>
              </div>
              <p className="text-muted-foreground text-xs">
                For GitHub, use a fine-grained personal access token with access to this repository;
                any username works. The token is encrypted, stays in the harness, and never reaches
                a sandbox.
              </p>
            </div>
          )}
        </div>
      ) : (
        <div className="space-y-1.5">
          <Label htmlFor="project-path">Path on the Docker host</Label>
          <Input
            id="project-path"
            value={hostPath}
            placeholder="/home/you/code/my-app"
            autoComplete="off"
            className="font-mono"
            onChange={(e) => {
              setHostPath(e.target.value);
            }}
          />
          <p className="text-muted-foreground text-xs">
            An absolute path on the machine Docker runs on. Workspaces mount it, so agents edit it
            in place.
          </p>
        </div>
      )}

      {problem !== null && touched && <p className="text-muted-foreground text-xs">{problem}</p>}
      {create.isError && <ActionError action="add the project" error={create.error} />}
      {create.isPending && kind === "remote" && (
        <Notice tone="pending">Mirroring the repository into the hub…</Notice>
      )}

      <div className="flex flex-wrap justify-end gap-2">
        {onCancel && (
          <Button type="button" variant="ghost" onClick={onCancel}>
            {cancelLabel ?? "Cancel"}
          </Button>
        )}
        <Button type="submit" disabled={problem !== null || create.isPending}>
          {create.isPending ? "Adding…" : (submitLabel ?? "Add project")}
        </Button>
      </div>
    </form>
  );
}
