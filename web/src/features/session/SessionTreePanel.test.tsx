import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { Root } from "react-dom/client";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { connect, disconnect } from "@/api/connection";
import { SessionTreePanel } from "@/features/session/SessionTreePanel";

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean;
}

const at = "2026-03-14T15:00:00Z";
// a - b - c (head), with a branch x off b.
const outline = {
  session_id: "s1",
  head_entry_id: "c",
  nodes: [
    { id: "a", kind: "user", preview: "Fix the retries", created_at: at },
    { id: "b", parent_id: "a", kind: "assistant", preview: "Looking", created_at: at },
    { id: "c", parent_id: "b", kind: "assistant", preview: "Done", created_at: at },
    { id: "x", parent_id: "b", kind: "user", preview: "Try again", created_at: at },
  ],
};

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;
  connect({ baseUrl: "", token: "token" });
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.resolve(new Response(JSON.stringify(outline), { status: 200 }))),
  );
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => {
    root.unmount();
  });
  container.remove();
  disconnect();
  vi.unstubAllGlobals();
});

async function render() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root.render(
      <QueryClientProvider client={client}>
        <MemoryRouter>
          <SessionTreePanel sessionId="s1" />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    await Promise.resolve();
  });
  for (let i = 0; i < 50 && items().length === 0; i++) {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10));
    });
  }
}

function items(): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>('[role="treeitem"]'));
}

function press(key: string) {
  const target = document.activeElement;
  if (!(target instanceof HTMLElement)) throw new Error("nothing is focused");
  act(() => {
    target.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true }));
  });
}

describe("SessionTreePanel", () => {
  it("marks up the outline as a tree of levelled items", async () => {
    await render();
    const tree = container.querySelector('[role="tree"]');
    expect(tree?.getAttribute("aria-label")).toBe("Session tree");
    expect(
      items().map((el) => [
        el.getAttribute("aria-label"),
        el.getAttribute("aria-level"),
        el.getAttribute("aria-posinset"),
        el.getAttribute("aria-setsize"),
      ]),
    ).toEqual([
      ["user: Fix the retries", "1", "1", "3"],
      ["assistant: Looking", "1", "2", "3"],
      ["user: Try again", "2", "1", "1"],
      ["assistant: Done (head)", "1", "3", "3"],
    ]);
    expect(items()[3]?.getAttribute("aria-current")).toBe("true");
  });

  it("is one tab stop, starting on the head, and moves with the arrow keys", async () => {
    await render();
    expect(items().map((el) => el.tabIndex)).toEqual([-1, -1, -1, 0]);
    items()[3]?.focus();
    press("Home");
    expect(document.activeElement).toBe(items()[0]);
    press("ArrowDown");
    press("ArrowRight");
    expect(document.activeElement).toBe(items()[2]);
    press("ArrowLeft");
    expect(document.activeElement).toBe(items()[1]);
    press("End");
    expect(document.activeElement).toBe(items()[3]);
    expect(items().map((el) => el.tabIndex)).toEqual([-1, -1, -1, 0]);
  });

  it("asks before moving the head on Enter", async () => {
    await render();
    items()[3]?.focus();
    press("Home");
    press("Enter");
    expect(document.body.textContent).toContain("Move the head here?");
  });
});
