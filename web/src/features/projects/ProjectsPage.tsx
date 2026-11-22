import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";

import { ApiError } from "@/api/client";
import { Link, useParams } from "react-router";

import { queryKeys } from "@/api/keys";
import {
  getProjectInstructions,
  listProjects,
  listSessions,
  putProjectInstructions,
} from "@/api/routes";
import type { Project, ProjectInstructions } from "@/api/types";
import { ActionError, LoadError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { textProblem } from "@/lib/textLimit";

export function ProjectsPage() {
  const { slug } = useParams();
  const projects = useQuery({
    queryKey: queryKeys.projects(),
    queryFn: ({ signal }) => listProjects(signal),
    refetchInterval: 10000,
  });
  const sessions = useQuery({
    queryKey: queryKeys.allSessions(),
    queryFn: ({ signal }) => listSessions(signal),
  });
  if (projects.isPending)
    return (
      <div className="p-4">
        <Notice tone="pending">Loading projects…</Notice>
      </div>
    );
  if (projects.isError && !projects.data)
    return (
      <div className="p-4">
        <LoadError
          what="the projects"
          error={projects.error}
          retrying={projects.isFetching}
          retry={() => void projects.refetch()}
        />
      </div>
    );
  const project = projects.data.find((p) => p.slug === slug);
  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto max-w-3xl space-y-4 p-4">
        {slug === undefined ? (
          <>
            <h2 className="text-lg font-semibold">Projects</h2>
            {projects.data.length === 0 ? (
              <p className="text-muted-foreground text-sm">No projects yet.</p>
            ) : (
              <ul className="divide-y rounded-md border">
                {projects.data.map((p) => (
                  <li key={p.id}>
                    <Link
                      className="hover:bg-accent block p-3"
                      to={`/projects/${encodeURIComponent(p.slug)}`}
                    >
                      <span className="font-medium">{p.name}</span>{" "}
                      {sessions.data?.some(
                        (s) =>
                          s.kind === "subagent" && s.project_id === p.id && s.state === "running",
                      ) && <span className="text-primary text-xs">● Subagent running</span>}{" "}
                      <span className="text-muted-foreground text-xs">{p.status}</span>
                      <p className="text-muted-foreground truncate text-sm">{p.description}</p>
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </>
        ) : project === undefined ? (
          <>
            <Link to="/projects" className="touch-link text-primary text-sm">
              Projects
            </Link>
            <p>Project not found.</p>
          </>
        ) : (
          <ProjectDetail key={project.id} project={project} />
        )}
      </div>
    </div>
  );
}

function ProjectDetail({ project }: { project: Project }) {
  const [tab, setTab] = useState("Overview");
  return (
    <>
      <Link to="/projects" className="touch-link text-primary text-sm">
        ← Projects
      </Link>
      <h2 className="text-lg font-semibold">{project.name}</h2>
      <div className="flex gap-2 border-b" role="group" aria-label="Project sections">
        {["Overview", "Instructions", "Subagents"].map((name) => (
          <button
            key={name}
            aria-pressed={tab === name}
            className="hover:bg-accent rounded-md px-3 py-2 text-sm aria-pressed:font-semibold"
            onClick={() => {
              setTab(name);
            }}
          >
            {name}
          </button>
        ))}
      </div>
      {tab === "Overview" && (
        <div className="space-y-3 text-sm">
          <p>{project.description || "No description."}</p>
          <p>Status: {project.status}</p>
          <p>
            Directory: <span className="font-mono">{project.directory}</span>
          </p>
        </div>
      )}
      <div hidden={tab !== "Instructions"}>
        <Instructions id={project.id} />
      </div>
      {tab === "Subagents" && <Subagents id={project.id} />}
    </>
  );
}

function Instructions({ id }: { id: string }) {
  const client = useQueryClient();
  const query = useQuery({
    queryKey: queryKeys.projectInstructions(id),
    queryFn: ({ signal }) => getProjectInstructions(id, signal),
  });
  const save = useMutation({
    mutationFn: (input: ProjectInstructions) => putProjectInstructions(id, input),
    onSuccess: (data) => client.setQueryData(queryKeys.projectInstructions(id), data),
  });
  if (query.isPending) return <Notice tone="pending">Loading instructions…</Notice>;
  if (!query.data)
    return (
      <LoadError
        what="the instructions"
        error={query.error}
        retrying={query.isFetching}
        retry={() => void query.refetch()}
      />
    );
  return (
    <>
      {query.isError && (
        <LoadError
          what="the latest instructions"
          error={query.error}
          retrying={query.isFetching}
          retry={() => void query.refetch()}
        />
      )}
      <InstructionsForm id={id} stored={query.data} save={save} />
    </>
  );
}

type Draft = { text: string; base: ProjectInstructions };

function InstructionsForm({
  id,
  stored,
  save,
}: {
  id: string;
  stored: ProjectInstructions;
  save: ReturnType<typeof useMutation<ProjectInstructions, Error, ProjectInstructions>>;
}) {
  const key = `project-instructions-draft:${id}`;
  const client = useQueryClient();
  const [loadError, setLoadError] = useState<unknown>(null);
  const [resolving, setResolving] = useState(false);
  const [draft, setDraft] = useState<Draft>(() => {
    try {
      const saved: unknown = JSON.parse(sessionStorage.getItem(key) ?? "null");
      if (
        typeof saved === "object" &&
        saved !== null &&
        "text" in saved &&
        typeof saved.text === "string" &&
        "base" in saved &&
        typeof saved.base === "object" &&
        saved.base !== null &&
        "version" in saved.base &&
        typeof saved.base.version === "number" &&
        "instructions" in saved.base &&
        typeof saved.base.instructions === "string"
      )
        return saved as Draft;
    } catch {
      /* Ignore an invalid draft. */
    }
    return { text: stored.instructions, base: stored };
  });
  const [seen, setSeen] = useState(stored);
  if (seen.version !== stored.version) {
    setSeen(stored);
    if (draft.text === draft.base.instructions)
      setDraft({ text: stored.instructions, base: stored });
  }
  const dirty = draft.text !== draft.base.instructions;
  useEffect(() => {
    if (dirty) sessionStorage.setItem(key, JSON.stringify(draft));
    else sessionStorage.removeItem(key);
    const warn = (event: BeforeUnloadEvent) => {
      if (dirty || save.isPending) event.preventDefault();
    };
    window.addEventListener("beforeunload", warn);
    return () => {
      window.removeEventListener("beforeunload", warn);
    };
  }, [key, draft, dirty, save.isPending]);
  const resolve = async (overwrite: boolean) => {
    setResolving(true);
    setLoadError(null);
    try {
      const latest = await getProjectInstructions(id);
      client.setQueryData(queryKeys.projectInstructions(id), latest);
      if (overwrite) submit(latest);
      else {
        setDraft({ text: latest.instructions, base: latest });
        save.reset();
      }
    } catch (error) {
      setLoadError(error);
    } finally {
      setResolving(false);
    }
  };
  const problem = textProblem(draft.text);
  const conflict = save.error instanceof ApiError && save.error.status === 409;
  const submit = (base: ProjectInstructions) => {
    const text = draft.text;
    save.mutate(
      { instructions: text, version: base.version },
      {
        onSuccess: (result) => {
          setDraft((current) => ({ text: current.text, base: result }));
        },
      },
    );
  };
  return (
    <form
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (dirty && !problem && !conflict) submit(draft.base);
      }}
    >
      <p className="text-muted-foreground text-sm">
        Only you can edit these instructions. Project subagents read them at the start of a
        conversation and after context compaction.
      </p>
      <Label htmlFor="project-instructions">Instructions</Label>
      <Textarea
        id="project-instructions"
        value={draft.text}
        rows={10}
        className="font-mono text-xs"
        aria-invalid={problem !== ""}
        aria-describedby={problem ? "project-instructions-error" : undefined}
        onChange={(e) => {
          setDraft({ ...draft, text: e.target.value });
        }}
      />
      {problem && (
        <p id="project-instructions-error" className="text-destructive text-xs">
          {problem}
        </p>
      )}
      {conflict ? (
        <Notice tone="warning">
          These instructions changed elsewhere. Reload to discard your draft, or overwrite the
          latest text.
        </Notice>
      ) : (
        save.isError && <ActionError action="save the instructions" error={save.error} />
      )}
      {loadError !== null && (
        <ActionError action="load the latest instructions" error={loadError} />
      )}
      {conflict && (
        <div className="flex gap-2">
          <Button
            type="button"
            variant="outline"
            disabled={save.isPending || resolving}
            onClick={() => {
              void resolve(false);
            }}
          >
            Reload instructions
          </Button>
          <Button
            type="button"
            variant="outline"
            disabled={save.isPending || resolving || !!problem}
            onClick={() => {
              void resolve(true);
            }}
          >
            Overwrite
          </Button>
        </div>
      )}
      {save.isSuccess && !dirty && <Notice tone="success">Saved.</Notice>}
      <div className="flex justify-end gap-2">
        <Button
          type="button"
          variant="ghost"
          disabled={!dirty || save.isPending}
          onClick={() => {
            setDraft({ text: stored.instructions, base: stored });
            save.reset();
          }}
        >
          Discard changes
        </Button>
        <Button type="submit" disabled={!dirty || !!problem || save.isPending || conflict}>
          {save.isPending ? "Saving…" : "Save instructions"}
        </Button>
      </div>
    </form>
  );
}

function Subagents({ id }: { id: string }) {
  const agents = useQuery({
    queryKey: queryKeys.allSessions(),
    queryFn: ({ signal }) => listSessions(signal),
  });
  if (agents.isPending) return <Notice tone="pending">Loading subagents…</Notice>;
  if (agents.isError)
    return (
      <LoadError
        what="the subagents"
        error={agents.error}
        retrying={agents.isFetching}
        retry={() => void agents.refetch()}
      />
    );
  const rows = agents.data.filter((s) => s.kind === "subagent" && s.project_id === id);
  if (!rows.length)
    return <p className="text-muted-foreground text-sm">No subagents in this project.</p>;
  return (
    <ul className="divide-y rounded-md border">
      {rows.map((s) => (
        <li key={s.id}>
          <Link
            className="hover:bg-accent block p-3 text-sm"
            to={`/activity/${encodeURIComponent(s.id)}`}
          >
            <span className="font-medium">{s.name ?? s.title}</span>{" "}
            <span className="text-muted-foreground">
              {s.agent} · {s.state}
            </span>
          </Link>
        </li>
      ))}
    </ul>
  );
}
