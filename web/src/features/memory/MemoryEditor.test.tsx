import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { Root } from "react-dom/client";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "@/api/client";
import type { Memory } from "@/api/types";
import { MemoryEditor } from "@/features/memory/MemoryEditor";

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean;
}

const routes = vi.hoisted(() => ({
  editMemory: vi.fn(),
  getMemory: vi.fn(),
  setMemoryPinned: vi.fn(),
  archiveMemory: vi.fn(),
  purgeMemory: vi.fn(),
  listMemories: vi.fn(),
  listSessions: vi.fn(),
}));
vi.mock("@/api/routes", () => routes);

const stored: Memory = {
  name: "tea-order",
  type: "preference",
  description: "Drinks oolong in the morning and no caffeine after 3pm.",
  source: "",
  created: "2026-11-01T10:00:00Z",
  updated: "2026-11-02T10:00:00Z",
  expires: "",
  pinned: false,
  state: "active",
  body: "Morning: oolong.",
  content_sha: "sha-1",
};
const stale = () =>
  new ApiError(409, "conflict", "this memory changed since it was read; reload it and try again");

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
  })) as unknown as typeof window.matchMedia;
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
  act(() => {
    root.render(
      <QueryClientProvider client={new QueryClient()}>
        <MemoryRouter>
          <MemoryEditor memory={stored} />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
});

afterEach(() => {
  act(() => {
    root.unmount();
  });
  container.remove();
  vi.resetAllMocks();
});

const settle = () =>
  act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 10));
  });
const body = () => container.querySelector<HTMLTextAreaElement>("#memory-body");
const button = (name: string) =>
  [...document.querySelectorAll("button")].find((b) => b.textContent === name);

function typeBody(value: string) {
  const el = body();
  if (!el) throw new Error("no body field");
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.call(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

async function click(name: string) {
  const el = button(name);
  if (!el) throw new Error(`no button ${name}`);
  act(() => {
    el.click();
  });
  await settle();
}

describe("MemoryEditor", () => {
  it("sends only what changed with the sha it read, and says when the edit went pending", async () => {
    routes.editMemory.mockResolvedValue({ ...stored, state: "pending", content_sha: "sha-2" });
    expect(button("Save memory")?.disabled).toBe(true);
    typeBody("Morning: oolong. Evening: nothing.");
    await click("Save memory");
    expect(routes.editMemory).toHaveBeenCalledWith("tea-order", {
      content_sha: "sha-1",
      body: "Morning: oolong. Evening: nothing.",
    });
    expect(container.textContent).toContain("now pending and unpinned");
  });

  it("on a stale edit shows the current version, and reapplying sends the draft on the new sha", async () => {
    routes.editMemory.mockRejectedValueOnce(stale());
    routes.getMemory.mockResolvedValue({
      ...stored,
      body: "Changed elsewhere.",
      content_sha: "sha-9",
    });
    typeBody("My draft.");
    await click("Save memory");
    expect(container.textContent).toContain("Nothing was saved");
    expect(container.textContent).toContain("Changed elsewhere.");
    expect(body()?.value).toBe("My draft.");
    routes.editMemory.mockResolvedValue({ ...stored, body: "My draft.", content_sha: "sha-10" });
    await click("Keep my edits and save again");
    await click("Save memory");
    expect(routes.editMemory).toHaveBeenLastCalledWith("tea-order", {
      content_sha: "sha-9",
      body: "My draft.",
    });
  });

  it("refuses a credential and a 503 in plain words, and keeps the draft", async () => {
    routes.editMemory.mockRejectedValueOnce(
      new ApiError(400, "invalid_request", "this memory contains a credential in its body."),
    );
    typeBody("key AKIAABCDEFGHIJKLMNOP");
    await click("Save memory");
    expect(container.textContent).toContain("contains a credential");
    routes.editMemory.mockRejectedValueOnce(
      new ApiError(503, "internal", "memory is not available yet"),
    );
    await click("Save memory");
    expect(container.textContent).toContain("nothing was changed");
    expect(body()?.value).toBe("key AKIAABCDEFGHIJKLMNOP");
  });

  it("purges only after a confirmation that says history and backups may keep the text", async () => {
    routes.purgeMemory.mockResolvedValue(undefined);
    await click("Purge");
    expect(routes.purgeMemory).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("git history");
    expect(document.body.textContent).toContain("backups");
    await click("Purge memory");
    expect(routes.purgeMemory).toHaveBeenCalledWith("tea-order", "sha-1");
  });
});
