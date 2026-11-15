import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Agent } from "@/api/types";
import { AgentsSettings } from "@/features/agents/AgentsSettings";

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean;
}

const routes = vi.hoisted(() => ({
  listAgents: vi.fn(),
  putAgent: vi.fn(),
  resetAgent: vi.fn(),
  getSettings: vi.fn(),
}));
vi.mock("@/api/routes", () => routes);

const builtin = {
  name: "eika",
  description: "Converses.",
  class: "main" as const,
  prompt: "Built in.",
  tools: ["web_search"],
  max_turns: 40,
  compaction: { summary: "", update: "", turn_prefix: "" },
};
const agent = (overrides: Agent["overrides"]): Agent => ({
  ...builtin,
  ...(overrides.class === null ? {} : { class: overrides.class }),
  builtin,
  overrides,
});

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;
  routes.getSettings.mockResolvedValue({ settings: {}, defaults: {}, classes: [] });
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => {
    root.unmount();
  });
  container.remove();
  document.body.replaceChildren();
  vi.resetAllMocks();
});

async function render(stored: Agent) {
  routes.listAgents.mockResolvedValue([stored]);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root.render(
      <QueryClientProvider client={client}>
        <AgentsSettings />
      </QueryClientProvider>,
    );
  });
  for (let i = 0; i < 50 && button(/^eika/) === undefined; i++) {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10));
    });
  }
}

const button = (name: RegExp | string) =>
  [...document.querySelectorAll("button")].find((b) =>
    typeof name === "string" ? b.textContent === name : name.test(b.textContent),
  );

async function click(el: Element | undefined) {
  if (!el) throw new Error("nothing to click");
  await act(async () => {
    (el as HTMLElement).click();
    await new Promise((resolve) => setTimeout(resolve, 10));
  });
}

/** isDisabled says whether the named button is on the page and disabled. */
const isDisabled = (name: string) => button(name)?.disabled === true;

const radio = (name: string) =>
  [...document.querySelectorAll('[role="radio"]')].find((r) => r.textContent === name);

describe("an agent's editor", () => {
  it("holds Reset while a save is pending, so the last action is what is stored", async () => {
    await render(agent({ class: "deep", prompt: null }));
    await click(button(/^eika/));
    let finish: (a: Agent) => void = () => undefined;
    routes.putAgent.mockReturnValue(
      new Promise((resolve) => {
        finish = resolve;
      }),
    );
    await click(radio("Fast"));
    await click(button("Save agent"));
    expect(routes.putAgent).toHaveBeenCalledTimes(1);
    expect(isDisabled("Reset to built-in")).toBe(true);
    await click(button("Reset to built-in"));
    expect(document.querySelector('[role="alertdialog"]')).toBeNull();
    expect(routes.resetAgent).not.toHaveBeenCalled();
    await act(async () => {
      finish(agent({ class: "fast", prompt: null }));
      await new Promise((resolve) => setTimeout(resolve, 10));
    });
  });

  it("holds Save while a reset is pending", async () => {
    await render(agent({ class: "deep", prompt: null }));
    await click(button(/^eika/));
    let finish: () => void = () => undefined;
    routes.resetAgent.mockReturnValue(
      new Promise<void>((resolve) => {
        finish = resolve;
      }),
    );
    await click(radio("Fast"));
    await click(button("Reset to built-in"));
    await click(
      [...document.querySelectorAll('[role="alertdialog"] button')].find(
        (b) => b.textContent === "Reset",
      ),
    );
    expect(routes.resetAgent).toHaveBeenCalledTimes(1);
    expect(isDisabled("Save agent")).toBe(true);
    expect(isDisabled("Reset to built-in")).toBe(true);
    await click(button("Save agent"));
    expect(routes.putAgent).not.toHaveBeenCalled();
    await act(async () => {
      finish();
      await new Promise((resolve) => setTimeout(resolve, 10));
    });
  });
});
