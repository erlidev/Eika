/**
 * One function per route in docs/api/http.md. They add nothing but the path,
 * the method, and the response type; policy lives in the features that call
 * them.
 */

import { request, requestEmpty } from "@/api/client";
import type {
  CreateProject,
  CreateWorkspace,
  Entry,
  Models,
  PostMessage,
  Project,
  Run,
  RunStatus,
  Session,
  SessionOutline,
  SessionPath,
  Settings,
  Workspace,
  WorkspaceDiff,
} from "@/api/types";

/** listProjects lists every project, newest first. */
export async function listProjects(signal?: AbortSignal): Promise<Project[]> {
  const body = await request<{ projects: Project[] }>("/api/projects", { signal });
  return body.projects;
}

/** createProject registers a repository with Eika. */
export function createProject(input: CreateProject): Promise<Project> {
  return request<Project>("/api/projects", { method: "POST", body: input });
}

/** getProject reads one project. */
export function getProject(id: string, signal?: AbortSignal): Promise<Project> {
  return request<Project>(`/api/projects/${encodeURIComponent(id)}`, { signal });
}

/** deleteProject destroys a project with its workspaces and sessions. */
export function deleteProject(id: string): Promise<void> {
  return requestEmpty(`/api/projects/${encodeURIComponent(id)}`, { method: "DELETE" });
}

/** listWorkspaces lists workspaces, optionally of one project. */
export async function listWorkspaces(
  projectId?: string,
  signal?: AbortSignal,
): Promise<Workspace[]> {
  const body = await request<{ workspaces: Workspace[] }>("/api/workspaces", {
    query: { project_id: projectId },
    signal,
  });
  return body.workspaces;
}

/** createWorkspace creates and starts a sandbox holding a project checkout. */
export function createWorkspace(input: CreateWorkspace): Promise<Workspace> {
  return request<Workspace>("/api/workspaces", { method: "POST", body: input });
}

/** getWorkspace reads one workspace. */
export function getWorkspace(id: string, signal?: AbortSignal): Promise<Workspace> {
  return request<Workspace>(`/api/workspaces/${encodeURIComponent(id)}`, { signal });
}

/** startWorkspace starts a stopped container again. */
export function startWorkspace(id: string): Promise<Workspace> {
  return request<Workspace>(`/api/workspaces/${encodeURIComponent(id)}/start`, { method: "POST" });
}

/** stopWorkspace aborts the workspace's runs and stops its container. */
export function stopWorkspace(id: string): Promise<Workspace> {
  return request<Workspace>(`/api/workspaces/${encodeURIComponent(id)}/stop`, { method: "POST" });
}

/** deleteWorkspace destroys the container, its volume, and its sessions. */
export function deleteWorkspace(id: string): Promise<void> {
  return requestEmpty(`/api/workspaces/${encodeURIComponent(id)}`, { method: "DELETE" });
}

/** getWorkspaceDiff reads the workspace's changes against its base commit. */
export function getWorkspaceDiff(id: string, signal?: AbortSignal): Promise<WorkspaceDiff> {
  return request<WorkspaceDiff>(`/api/workspaces/${encodeURIComponent(id)}/diff`, { signal });
}

/** listSessions lists sessions, optionally of one workspace. */
export async function listSessions(workspaceId?: string, signal?: AbortSignal): Promise<Session[]> {
  const body = await request<{ sessions: Session[] }>("/api/sessions", {
    query: { workspace_id: workspaceId },
    signal,
  });
  return body.sessions;
}

/** createSession opens a session in a workspace. */
export function createSession(input: { workspace_id: string; title: string }): Promise<Session> {
  return request<Session>("/api/sessions", { method: "POST", body: input });
}

/** getSession reads a session with the entry its head points at. */
export function getSession(
  id: string,
  signal?: AbortSignal,
): Promise<{ session: Session; head?: Entry }> {
  return request<{ session: Session; head?: Entry }>(`/api/sessions/${encodeURIComponent(id)}`, {
    signal,
  });
}

/** deleteSession aborts the session's run and deletes it with its entries. */
export function deleteSession(id: string): Promise<void> {
  return requestEmpty(`/api/sessions/${encodeURIComponent(id)}`, { method: "DELETE" });
}

/** getSessionOutline reads the whole tree without payloads. */
export function getSessionOutline(id: string, signal?: AbortSignal): Promise<SessionOutline> {
  return request<SessionOutline>(`/api/sessions/${encodeURIComponent(id)}/outline`, { signal });
}

/** getSessionPath reads the branch from the root to the head. */
export function getSessionPath(id: string, signal?: AbortSignal): Promise<SessionPath> {
  return request<SessionPath>(`/api/sessions/${encodeURIComponent(id)}/path`, { signal });
}

/** setSessionHead moves the head so the next run continues from an entry. */
export function setSessionHead(id: string, entryId: string): Promise<Session> {
  return request<Session>(`/api/sessions/${encodeURIComponent(id)}/head`, {
    method: "POST",
    body: { entry_id: entryId },
  });
}

/** forkSession copies the path down to an entry into a session of its own. */
export function forkSession(id: string, entryId: string, title: string): Promise<Session> {
  return request<Session>(`/api/sessions/${encodeURIComponent(id)}/fork`, {
    method: "POST",
    body: { entry_id: entryId, title },
  });
}

/** postMessage starts, steers, or queues a follow-up for a session's run. */
export function postMessage(id: string, input: PostMessage): Promise<Run> {
  return request<Run>(`/api/sessions/${encodeURIComponent(id)}/messages`, {
    method: "POST",
    body: input,
  });
}

/** getRunStatus reads what the session is doing and what waits for it. */
export function getRunStatus(id: string, signal?: AbortSignal): Promise<RunStatus> {
  return request<RunStatus>(`/api/sessions/${encodeURIComponent(id)}/run`, { signal });
}

/** abortRun cancels a run and waits for it to stop. */
export function abortRun(id: string): Promise<Run> {
  return request<Run>(`/api/runs/${encodeURIComponent(id)}/abort`, { method: "POST" });
}

/** answerQuestion delivers the answer an `ask_user` call is blocked on. */
export function answerQuestion(id: string, answer: string): Promise<void> {
  return requestEmpty(`/api/questions/${encodeURIComponent(id)}/answer`, {
    method: "POST",
    body: { answer },
  });
}

/** getSettings reads the settings table as one object. */
export async function getSettings(signal?: AbortSignal): Promise<Settings> {
  const body = await request<{ settings: Settings }>("/api/settings", { signal });
  return body.settings;
}

/** putSettings writes the named keys and leaves the rest alone. */
export async function putSettings(values: Settings): Promise<Settings> {
  const body = await request<{ settings: Settings }>("/api/settings", {
    method: "PUT",
    body: values,
  });
  return body.settings;
}

/** getModels lists the configured models and the chosen default. */
export function getModels(signal?: AbortSignal): Promise<Models> {
  return request<Models>("/api/models", { signal });
}
