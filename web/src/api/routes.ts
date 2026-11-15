/**
 * One function per route in docs/api/http.md. They add nothing but the path,
 * the method, and the response type; policy lives in the features that call
 * them.
 */

import { request, requestEmpty } from "@/api/client";
import type { Connection } from "@/api/connection";
import type {
  Agent,
  AgentOverride,
  Agents,
  AuthStatus,
  CompactRequest,
  Computer,
  ContentDetail,
  CreateMCPServer,
  CreateModel,
  CreateProvider,
  CreateSession,
  ElicitationAnswer,
  Entry,
  GlobalInstructions,
  MCPPromptResult,
  MCPServer,
  MCPServerDetails,
  Model,
  ModelContext,
  ModelCall,
  ModelCalls,
  NextPrediction,
  Models,
  OAuthCallback,
  PostMessage,
  ProbeProvider,
  ProbeResponse,
  Project,
  Projects,
  Provider,
  Providers,
  Run,
  RunStatus,
  SearchKey,
  SearchOutcome,
  SearchRequest,
  SearchStatus,
  Session,
  SessionConfiguration,
  SessionOutline,
  Settings,
  SettingsState,
  SignIn,
  SystemStatus,
  TestModel,
  TestModelResult,
  Tool,
  UpdateMCPServer,
  UpdateModel,
  UpdateProvider,
  UpdateSession,
} from "@/api/types";

/** listChats lists the sessions with no computer. */
export function getHome(): Promise<{ session: Session; head?: Entry }> {
  return request<{ session: Session; head?: Entry }>("/api/home");
}

export async function listChats(signal?: AbortSignal): Promise<Session[]> {
  const body = await request<{ sessions: Session[] }>("/api/sessions", {
    query: { chats: "true" },
    signal,
  });
  return body.sessions;
}

/** listSessions lists every session, oldest first: chats, computer sessions, home and subagents. */
export async function listSessions(signal?: AbortSignal): Promise<Session[]> {
  const body = await request<{ sessions: Session[] }>("/api/sessions", { signal });
  return body.sessions;
}

/** listProjects lists the projects, by slug. */
export async function listProjects(signal?: AbortSignal): Promise<Project[]> {
  const body = await request<Projects>("/api/projects", { signal });
  return body.projects;
}

/** listComputers lists the computers, oldest first. */
export async function listComputers(signal?: AbortSignal): Promise<Computer[]> {
  const body = await request<{ computers: Computer[] }>("/api/computers", { signal });
  return body.computers;
}

/** listComputerSessions lists the sessions that run in one computer. */
export async function listComputerSessions(
  computerId: string,
  signal?: AbortSignal,
): Promise<Session[]> {
  const body = await request<{ sessions: Session[] }>("/api/sessions", {
    query: { computer_id: computerId },
    signal,
  });
  return body.sessions;
}

/** createSession opens a session in a computer, or a chat in none. */
export function createSession(input: CreateSession): Promise<Session> {
  return request<Session>("/api/sessions", { method: "POST", body: input });
}

/** listTools lists every tool a run can offer, with whether it needs a computer. */
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

/** updateSession renames, pins, or archives a session. */
export function updateSession(id: string, input: UpdateSession): Promise<Session> {
  return request<Session>(`/api/sessions/${encodeURIComponent(id)}`, {
    method: "PATCH",
    body: input,
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

/** setSessionHead moves the head so the next run continues from an entry. */
export function setSessionHead(id: string, entryId: string): Promise<Session> {
  return request<Session>(`/api/sessions/${encodeURIComponent(id)}/head`, {
    method: "POST",
    body: { entry_id: entryId },
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

/**
 * compactSession summarizes the older part of a session's conversation, as a
 * run of its own that the event stream reports on.
 */
export function compactSession(id: string, input: CompactRequest): Promise<Run> {
  return request<Run>(`/api/sessions/${encodeURIComponent(id)}/compact`, {
    method: "POST",
    body: input,
  });
}

/** cancelSubagent stops a subagent's assignment and returns it as it ended. */
export function cancelSubagent(id: string): Promise<Session> {
  return request<Session>(`/api/sessions/${encodeURIComponent(id)}/cancel`, { method: "POST" });
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

/** getSessionConfiguration reads what a session's next run resolves to. */
export function getSessionConfiguration(
  id: string,
  signal?: AbortSignal,
): Promise<SessionConfiguration> {
  return request<SessionConfiguration>(`/api/sessions/${encodeURIComponent(id)}/configuration`, {
    signal,
  });
}

/**
 * getSessionContext previews the session's next model request, as a run
 * that names `model` ("" for none) would send it.
 */
export function getSessionContext(
  id: string,
  model: string,
  signal?: AbortSignal,
): Promise<ModelContext> {
  return request<ModelContext>(`/api/sessions/${encodeURIComponent(id)}/context`, {
    query: { model: model === "" ? undefined : model },
    signal,
  });
}

/**
 * getSessionPrediction asks what the session's next request is expected to
 * cost and how that is known, as a run that names `model` ("" for none)
 * would send it, without the request itself.
 */
export function getSessionPrediction(
  id: string,
  model: string,
  signal?: AbortSignal,
): Promise<NextPrediction> {
  return request<NextPrediction>(`/api/sessions/${encodeURIComponent(id)}/prediction`, {
    query: { model: model === "" ? undefined : model },
    signal,
  });
}

/** listSessionCalls lists the records of a session's model calls, oldest first. */
export async function listSessionCalls(id: string, signal?: AbortSignal): Promise<ModelCall[]> {
  const body = await request<ModelCalls>(`/api/sessions/${encodeURIComponent(id)}/calls`, {
    signal,
  });
  return body.calls ?? [];
}

/** getSessionCall reads one recorded model call in the preview's shape. */
export function getSessionCall(
  id: string,
  callId: string,
  signal?: AbortSignal,
): Promise<ModelContext> {
  return request<ModelContext>(
    `/api/sessions/${encodeURIComponent(id)}/calls/${encodeURIComponent(callId)}`,
    { signal },
  );
}

/** listAgents lists the built-in agents with their overrides. */
export async function listAgents(signal?: AbortSignal): Promise<Agent[]> {
  const body = await request<Agents>("/api/agents", { signal });
  return body.agents;
}

/** putAgent replaces an agent's class and prompt overrides; null uses the built-in. */
export function putAgent(name: string, override: AgentOverride): Promise<Agent> {
  return request<Agent>(`/api/agents/${encodeURIComponent(name)}`, {
    method: "PUT",
    body: override,
  });
}

/** resetAgent removes both overrides, so the agent runs as built. */
export function resetAgent(name: string): Promise<void> {
  return requestEmpty(`/api/agents/${encodeURIComponent(name)}`, { method: "DELETE" });
}

/** getInstructions reads the user's global About me and rules. */
export function getInstructions(signal?: AbortSignal): Promise<GlobalInstructions> {
  return request<GlobalInstructions>("/api/instructions", { signal });
}

/** putInstructions replaces both global instruction texts. */
export function putInstructions(input: GlobalInstructions): Promise<GlobalInstructions> {
  return request<GlobalInstructions>("/api/instructions", { method: "PUT", body: input });
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

/**
 * probeProvider asks an endpoint which models it serves and what kind of
 * server it is. Probing a stored provider's own endpoint records the findings
 * on it.
 */
export function probeProvider(input: ProbeProvider): Promise<ProbeResponse> {
  return request<ProbeResponse>("/api/providers/probe", { method: "POST", body: input });
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

/** listMCPServers lists the configured MCP servers by name, with the state each is in. */
export async function listMCPServers(signal?: AbortSignal): Promise<MCPServer[]> {
  const body = await request<{ servers: MCPServer[] | null }>("/api/mcp/servers", { signal });
  return body.servers ?? [];
}

/** createMCPServer configures an MCP server; an enabled http one starts connecting. */
export function createMCPServer(input: CreateMCPServer): Promise<MCPServer> {
  return request<MCPServer>("/api/mcp/servers", { method: "POST", body: input });
}

/** getMCPServer reads everything the harness knows of one MCP server. */
export function getMCPServer(id: string, signal?: AbortSignal): Promise<MCPServerDetails> {
  return request<MCPServerDetails>(`/api/mcp/servers/${encodeURIComponent(id)}`, { signal });
}

/** updateMCPServer changes an MCP server; its connections end and start again. */
export function updateMCPServer(id: string, input: UpdateMCPServer): Promise<MCPServer> {
  return request<MCPServer>(`/api/mcp/servers/${encodeURIComponent(id)}`, {
    method: "PATCH",
    body: input,
  });
}

/** deleteMCPServer removes an MCP server with its credentials and connections. */
export function deleteMCPServer(id: string): Promise<void> {
  return requestEmpty(`/api/mcp/servers/${encodeURIComponent(id)}`, { method: "DELETE" });
}

/**
 * connectMCPServer drops a server's connection and connects again, in the
 * computer for a stdio server, and answers with how that went.
 */
export function connectMCPServer(id: string, computerId?: string): Promise<MCPServerDetails> {
  return request<MCPServerDetails>(`/api/mcp/servers/${encodeURIComponent(id)}/connect`, {
    method: "POST",
    body: computerId === undefined ? {} : { computer_id: computerId },
  });
}

/** authorizeMCPServer starts an OAuth authorization and answers with where to send the browser. */
export async function authorizeMCPServer(id: string, redirectUri: string): Promise<string> {
  const body = await request<{ authorization_url: string }>(
    `/api/mcp/servers/${encodeURIComponent(id)}/authorize`,
    { method: "POST", body: { redirect_uri: redirectUri } },
  );
  return body.authorization_url;
}

/** finishMCPAuthorization hands the authorization server's answer to the harness. */
export async function finishMCPAuthorization(input: OAuthCallback): Promise<string> {
  const body = await request<{ server_id: string }>("/api/mcp/oauth/callback", {
    method: "POST",
    body: input,
  });
  return body.server_id;
}

/** signOutMCPServer revokes and forgets a server's tokens. */
export function signOutMCPServer(id: string): Promise<void> {
  return requestEmpty(`/api/mcp/servers/${encodeURIComponent(id)}/authorization`, {
    method: "DELETE",
  });
}

/** readMCPResource reads one resource of a server. */
export async function readMCPResource(
  id: string,
  uri: string,
  computerId?: string,
): Promise<ContentDetail[]> {
  const body = await request<{ contents: ContentDetail[] | null }>(
    `/api/mcp/servers/${encodeURIComponent(id)}/resources/read`,
    {
      method: "POST",
      body: { uri, ...(computerId === undefined ? {} : { computer_id: computerId }) },
    },
  );
  return body.contents ?? [];
}

/** getMCPPrompt renders one prompt of a server with its arguments. */
export function getMCPPrompt(
  id: string,
  name: string,
  args: Record<string, string>,
  computerId?: string,
): Promise<MCPPromptResult> {
  return request<MCPPromptResult>(`/api/mcp/servers/${encodeURIComponent(id)}/prompts/get`, {
    method: "POST",
    body: {
      name,
      arguments: args,
      ...(computerId === undefined ? {} : { computer_id: computerId }),
    },
  });
}

/** answerElicitation delivers the user's answer to an MCP tool call waiting on it. */
export function answerElicitation(id: string, answer: ElicitationAnswer): Promise<void> {
  return requestEmpty(`/api/elicitations/${encodeURIComponent(id)}/answer`, {
    method: "POST",
    body: answer,
  });
}
