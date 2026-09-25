/**
 * One function per route in docs/api/http.md. They add nothing but the path,
 * the method, and the response type; policy lives in the features that call
 * them.
 */

import { request, requestEmpty } from "@/api/client";
import type { Connection } from "@/api/connection";
import type {
  AuthStatus,
  CommitRequest,
  CommitResult,
  CreateModel,
  CreateProject,
  CreateProvider,
  CreateSession,
  CreateWorkspace,
  Entry,
  FileContent,
  FileEntry,
  Model,
  ModelInfo,
  Models,
  PostMessage,
  ProbeProvider,
  Project,
  Provider,
  Providers,
  PushRequest,
  PushResult,
  Run,
  RunStatus,
  SearchKey,
  SearchOutcome,
  SearchRequest,
  SearchStatus,
  Session,
  SessionOutline,
  SessionPath,
  Settings,
  SettingsState,
  SignIn,
  SystemStatus,
  TestModel,
  TestModelResult,
  Tool,
  UpdateModel,
  UpdateProject,
  UpdateProvider,
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

/** updateProject changes a project's remote credentials or default branch. */
export function updateProject(id: string, input: UpdateProject): Promise<Project> {
  return request<Project>(`/api/projects/${encodeURIComponent(id)}`, {
    method: "PATCH",
    body: input,
  });
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

/** listWorkspaceFiles lists one directory of a workspace; "" is the root. */
export async function listWorkspaceFiles(
  id: string,
  path: string,
  signal?: AbortSignal,
): Promise<FileEntry[]> {
  const body = await request<{ entries: FileEntry[] }>(
    `/api/workspaces/${encodeURIComponent(id)}/files`,
    { query: { path }, signal },
  );
  return body.entries;
}

/** readWorkspaceFile reads one text file of a workspace. */
export function readWorkspaceFile(
  id: string,
  path: string,
  signal?: AbortSignal,
): Promise<FileContent> {
  return request<FileContent>(`/api/workspaces/${encodeURIComponent(id)}/file`, {
    query: { path },
    signal,
  });
}

/** writeWorkspaceFile replaces a file's contents and returns its new entry. */
export function writeWorkspaceFile(id: string, path: string, content: string): Promise<FileEntry> {
  return request<FileEntry>(`/api/workspaces/${encodeURIComponent(id)}/file`, {
    method: "PUT",
    query: { path },
    text: content,
  });
}

/** commitWorkspace commits the workspace's changes, or only the given paths. */
export function commitWorkspace(id: string, input: CommitRequest): Promise<CommitResult> {
  return request<CommitResult>(`/api/workspaces/${encodeURIComponent(id)}/commit`, {
    method: "POST",
    body: input,
  });
}

/** pushWorkspace pushes the workspace's branch to the hub, and upstream when asked. */
export function pushWorkspace(id: string, input: PushRequest): Promise<PushResult> {
  return request<PushResult>(`/api/workspaces/${encodeURIComponent(id)}/push`, {
    method: "POST",
    body: input,
  });
}

/**
 * listSessions lists sessions, optionally of one workspace. `descendants`
 * adds the forks and child agents those sessions led to, which run in
 * workspaces of their own, so the sidebar draws the whole tree from one
 * request.
 */
export async function listSessions(
  workspaceId?: string,
  descendants?: boolean,
  signal?: AbortSignal,
): Promise<Session[]> {
  const body = await request<{ sessions: Session[] }>("/api/sessions", {
    query: { workspace_id: workspaceId, ...(descendants === true ? { descendants: "true" } : {}) },
    signal,
  });
  return body.sessions;
}

/** listChats lists the sessions with no workspace, forks of chats included. */
export async function listChats(signal?: AbortSignal): Promise<Session[]> {
  const body = await request<{ sessions: Session[] }>("/api/sessions", {
    query: { chats: "true" },
    signal,
  });
  return body.sessions;
}

/** createSession opens a session in a workspace, or a chat in none. */
export function createSession(input: CreateSession): Promise<Session> {
  return request<Session>("/api/sessions", { method: "POST", body: input });
}

/** setSessionTools chooses the tools the session's next run offers the model. */
export function setSessionTools(id: string, tools: string[]): Promise<Session> {
  return request<Session>(`/api/sessions/${encodeURIComponent(id)}/tools`, {
    method: "PUT",
    body: { tools },
  });
}

/** listTools lists every tool a run can offer, with whether it needs a workspace. */
export async function listTools(signal?: AbortSignal): Promise<Tool[]> {
  const body = await request<{ tools: Tool[] }>("/api/tools", { signal });
  return body.tools;
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

/** getSettings reads the settings table and the defaults of the harness's own keys. */
export function getSettings(signal?: AbortSignal): Promise<SettingsState> {
  return request<SettingsState>("/api/settings", { signal });
}

/** putSettings writes the named keys and leaves the rest alone. */
export function putSettings(values: Settings): Promise<SettingsState> {
  return request<SettingsState>("/api/settings", { method: "PUT", body: values });
}

/** getSearchStatus reports every search backend's health, the stored keys, and the caches. */
export function getSearchStatus(signal?: AbortSignal): Promise<SearchStatus> {
  return request<SearchStatus>("/api/search/status", { signal });
}

/** putSearchKey stores a search API key, or removes it when key is empty. */
export async function putSearchKey(name: string, key: string): Promise<SearchKey[]> {
  const body = await request<{ keys: SearchKey[] }>(
    `/api/search/keys/${encodeURIComponent(name)}`,
    {
      method: "PUT",
      body: { key },
    },
  );
  return body.keys;
}

/** runSearch runs one search as web_search would, spending quota like any other. */
export function runSearch(input: SearchRequest): Promise<SearchOutcome> {
  return request<SearchOutcome>("/api/search", { method: "POST", body: input });
}

/** getSystem reports whether Docker and the sandbox image are ready. */
export function getSystem(signal?: AbortSignal): Promise<SystemStatus> {
  return request<SystemStatus>("/api/system", { signal });
}

/** listProviders lists the model providers and the kinds a new one may have. */
export function listProviders(signal?: AbortSignal): Promise<Providers> {
  return request<Providers>("/api/providers", { signal });
}

/** createProvider records a provider; its key is sealed by the harness. */
export function createProvider(input: CreateProvider): Promise<Provider> {
  return request<Provider>("/api/providers", { method: "POST", body: input });
}

/** updateProvider changes a provider's name, endpoint, or key. */
export function updateProvider(id: string, input: UpdateProvider): Promise<Provider> {
  return request<Provider>(`/api/providers/${encodeURIComponent(id)}`, {
    method: "PATCH",
    body: input,
  });
}

/** deleteProvider removes a provider with its models. */
export function deleteProvider(id: string): Promise<void> {
  return requestEmpty(`/api/providers/${encodeURIComponent(id)}`, { method: "DELETE" });
}

/** probeProvider asks an endpoint which models it serves. */
export async function probeProvider(input: ProbeProvider): Promise<ModelInfo[]> {
  const body = await request<{ models: ModelInfo[] }>("/api/providers/probe", {
    method: "POST",
    body: input,
  });
  return body.models;
}

/** getModels lists the models and the one a run uses by default. */
export function getModels(signal?: AbortSignal): Promise<Models> {
  return request<Models>("/api/models", { signal });
}

/** createModel adds a model to a provider. */
export function createModel(input: CreateModel): Promise<Model> {
  return request<Model>("/api/models", { method: "POST", body: input });
}

/** updateModel changes a model. */
export function updateModel(id: string, input: UpdateModel): Promise<Model> {
  return request<Model>(`/api/models/${encodeURIComponent(id)}`, { method: "PATCH", body: input });
}

/** deleteModel removes a model. */
export function deleteModel(id: string): Promise<void> {
  return requestEmpty(`/api/models/${encodeURIComponent(id)}`, { method: "DELETE" });
}

/** testModel sends one small request to a model and reports its answer. */
export function testModel(input: TestModel): Promise<TestModelResult> {
  return request<TestModelResult>("/api/models/test", { method: "POST", body: input });
}

/**
 * getAuthStatus says whether the harness has been set up. It needs no token,
 * so it passes the connection explicitly: a stored token the harness refuses
 * must not be forgotten by a route that never needed it.
 */
export function getAuthStatus(connection: Connection, signal?: AbortSignal): Promise<AuthStatus> {
  return request<AuthStatus>("/api/auth/status", {
    connection: { ...connection, token: "" },
    signal,
  });
}

/** setupPassword chooses the sign-in password of a new harness and signs in. */
export function setupPassword(baseUrl: string, password: string): Promise<SignIn> {
  return request<SignIn>("/api/auth/setup", {
    method: "POST",
    body: { password },
    connection: { baseUrl, token: "" },
  });
}

/** signIn exchanges the password for a session token. */
export function signIn(baseUrl: string, password: string): Promise<SignIn> {
  return request<SignIn>("/api/auth/login", {
    method: "POST",
    body: { password },
    connection: { baseUrl, token: "" },
  });
}

/** signOut ends the session this browser holds. */
export function signOut(): Promise<void> {
  return requestEmpty("/api/auth/logout", { method: "POST" });
}

/** changePassword replaces the password, ending every session, and signs in again. */
export function changePassword(currentPassword: string, newPassword: string): Promise<SignIn> {
  return request<SignIn>("/api/auth/password", {
    method: "PUT",
    body: { current_password: currentPassword, new_password: newPassword },
  });
}
