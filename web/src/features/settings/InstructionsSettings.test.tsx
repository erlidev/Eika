import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { queryKeys } from "@/api/keys";
import { InstructionsSettings } from "@/features/settings/InstructionsSettings";

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean;
}

const routes = vi.hoisted(() => ({ getInstructions: vi.fn(), putInstructions: vi.fn() }));
vi.mock("@/api/routes", () => routes);

let container: HTMLDivElement;
let root: Root;
let client: QueryClient;

beforeEach(() => {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;
  routes.getInstructions.mockResolvedValue({ about_me: "stored me", rules: "stored rules" });
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
});

afterEach(() => {
  act(() => {
    root.unmount();
  });
  container.remove();
  vi.resetAllMocks();
});

async function render() {
  act(() => {
    root.render(
      <QueryClientProvider client={client}>
        <InstructionsSettings />
      </QueryClientProvider>,
    );
  });
  await until(() => field("instructions-about-me") !== null);
}

/** until lets the pending queries and renders finish. */
async function until(done: () => boolean) {
  for (let i = 0; i < 50 && !done(); i++) {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10));
    });
  }
}

const field = (id: string) => container.querySelector<HTMLTextAreaElement>(`#${id}`);

/** type sets a textarea's value as typing would, through React's own setter. */
async function type(id: string, value: string) {
  const el = field(id);
  if (!el) throw new Error(`no field ${id}`);
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.call(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 10));
  });
}

const button = (name: string) =>
  [...container.querySelectorAll("button")].find((b) => b.textContent === name);

async function click(name: string) {
  const el = button(name);
  if (!el) throw new Error(`no button ${name}`);
  await act(async () => {
    el.click();
    await new Promise((resolve) => setTimeout(resolve, 10));
  });
}

describe("InstructionsSettings", () => {
  it("keeps what was typed while a save is pending, and the save's text does not replace it", async () => {
    let finish: (v: { about_me: string; rules: string }) => void = () => undefined;
    routes.putInstructions.mockReturnValue(
      new Promise((resolve) => {
        finish = resolve;
      }),
    );
    await render();
    await type("instructions-about-me", "first");
    await click("Save instructions");
    expect(routes.putInstructions.mock.calls[0]?.[0]).toEqual({
      about_me: "first",
      rules: "stored rules",
    });
    await type("instructions-about-me", "first, then more");
    await act(async () => {
      finish({ about_me: "first", rules: "stored rules" });
      await new Promise((resolve) => setTimeout(resolve, 10));
    });
    expect(field("instructions-about-me")?.value).toBe("first, then more");
    // The edit is not saved: the form still offers to save it.
    expect(button("Save instructions")?.disabled).toBe(false);
  });

  it("keeps a draft when a refetch brings newer text, and follows it when there is none", async () => {
    await render();
    await type("instructions-rules", "my draft");
    await act(async () => {
      client.setQueryData(queryKeys.instructions(), {
        about_me: "from elsewhere",
        rules: "rules elsewhere",
      });
      await new Promise((resolve) => setTimeout(resolve, 10));
    });
    // The field with a draft keeps it; the untouched one follows the refetch.
    expect(field("instructions-rules")?.value).toBe("my draft");
    expect(field("instructions-about-me")?.value).toBe("from elsewhere");
    expect(button("Save instructions")?.disabled).toBe(false);
  });
});
