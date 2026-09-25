import { describe, expect, it } from "vitest";

import type { Session } from "@/api/types";
import { agentWorkspaces, sessionTree } from "@/features/sessions/tree";

function session(id: string, over: Partial<Session> = {}): Session {
  return {
    id,
    workspace_id: "ws-1",
    title: id,
    kind: "user",
    tools: [],
    overridden: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...over,
  };
}

/** shape renders a tree as `id(child, child)`, so a test reads as one line. */
function shape(nodes: ReturnType<typeof sessionTree>): string {
  return nodes
    .map((n) => (n.children.length === 0 ? n.session.id : `${n.session.id}(${shape(n.children)})`))
    .join(", ");
}

describe("sessionTree", () => {
  it("nests a fork and a child agent under the session they came from", () => {
    const tree = sessionTree([
      session("root"),
      session("fork", { kind: "fork", parent_session_id: "root" }),
      session("agent", { kind: "agent", parent_session_id: "root", workspace_id: "ws-2" }),
      session("deep", { kind: "agent", parent_session_id: "agent", workspace_id: "ws-3" }),
      session("other"),
    ]);
    expect(shape(tree)).toBe("root(fork, agent(deep)), other");
  });

  it("makes a session whose parent is not listed a root of its own", () => {
    // A fork whose parent was deleted keeps its kind; the pointer is cleared,
    // and a child of a session in another workspace is simply not here.
    const tree = sessionTree([
      session("orphan", { kind: "fork", parent_session_id: "gone" }),
      session("loose", { kind: "fork" }),
    ]);
    expect(shape(tree)).toBe("orphan, loose");
  });

  it("returns nothing for no sessions", () => {
    expect(sessionTree([])).toEqual([]);
  });
});

describe("agentWorkspaces", () => {
  it("names only the workspaces that hold no session of the user's", () => {
    const hidden = agentWorkspaces([
      session("root"),
      // A fork that kept its parent's workspace must not hide it.
      session("fork", { kind: "fork", parent_session_id: "root" }),
      session("agent", { kind: "agent", parent_session_id: "root", workspace_id: "ws-2" }),
      session("deep", { kind: "agent", parent_session_id: "agent", workspace_id: "ws-3" }),
    ]);
    expect([...hidden].sort()).toEqual(["ws-2", "ws-3"]);
  });

  it("keeps a workspace a user session was opened in", () => {
    // Adopting a child agent's workspace by starting a session in it puts it
    // back in the list.
    const hidden = agentWorkspaces([
      session("agent", { kind: "agent", workspace_id: "ws-2" }),
      session("mine", { workspace_id: "ws-2" }),
    ]);
    expect([...hidden]).toEqual([]);
  });

  it("keeps the workspace of a child whose parent session was deleted", () => {
    // Deleting the parent clears the child's pointer, so no tree reaches it
    // any more; hiding its workspace too would lose it altogether.
    const hidden = agentWorkspaces([
      session("root"),
      session("agent", { kind: "agent", workspace_id: "ws-2" }),
      session("fork", { kind: "fork", parent_session_id: "gone", workspace_id: "ws-3" }),
    ]);
    expect([...hidden]).toEqual([]);
  });

  it("keeps a workspace that has no sessions at all", () => {
    expect([...agentWorkspaces([])]).toEqual([]);
  });

  it("leaves chats out, since they are in no workspace", () => {
    const chat = session("chat", { workspace_id: undefined });
    const fork = session("chat-fork", {
      workspace_id: undefined,
      kind: "fork",
      parent_session_id: "chat",
    });
    expect([...agentWorkspaces([chat, fork])]).toEqual([]);
  });
});
