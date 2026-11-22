import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { SecuritySettings } from "@/features/settings/SecuritySettings";

const routes = vi.hoisted(() => ({
  getGuard: vi.fn().mockResolvedValue({ status: "down" }),
  getSettings: vi.fn().mockResolvedValue({
    settings: {},
    defaults: { screen_required: false, screen_threshold: 0.9 },
  }),
  getWithheld: vi.fn().mockResolvedValue({
    withheld: [
      {
        id: "1",
        source: "web_fetch",
        source_name: "https://example.com/page",
        at: "2026-11-22T00:00:00Z",
        text: "suspect",
      },
    ],
  }),
  getTrustedSources: vi.fn().mockResolvedValue({ hosts: ["example.org"] }),
  putSettings: vi.fn().mockResolvedValue({
    settings: { screen_required: true },
    defaults: { screen_required: false, screen_threshold: 0.9 },
  }),
  addTrustedSource: vi.fn().mockResolvedValue(undefined),
  removeTrustedSource: vi.fn().mockResolvedValue(undefined),
  trustWithheld: vi.fn().mockResolvedValue(undefined),
}));
vi.mock("@/api/routes", () => routes);

afterEach(() => {
  document.body.replaceChildren();
  vi.clearAllMocks();
});

it("saves screening and trusts or removes a source", async () => {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;
  const node = document.createElement("div");
  document.body.append(node);
  const root = createRoot(node);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root.render(
      <QueryClientProvider client={client}>
        <SecuritySettings />
      </QueryClientProvider>,
    );
  });
  for (let i = 0; i < 50 && !node.querySelector("#screen-required"); i++) {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10));
    });
  }
  const click = async (element: HTMLElement | null) => {
    if (!element) throw new Error("missing control");
    await act(async () => {
      element.click();
      await new Promise((resolve) => setTimeout(resolve, 10));
    });
  };
  const host = node.querySelector<HTMLInputElement>("#trusted-host");
  if (!host) throw new Error("missing host");
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(
      host,
      "new.example",
    );
    host.dispatchEvent(new Event("input", { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 10));
  });
  await click([...node.querySelectorAll("button")].find((b) => b.textContent === "Add") ?? null);
  expect(routes.addTrustedSource.mock.calls[0]?.[0]).toBe("new.example");
  await click(node.querySelector("#screen-required"));
  expect(routes.putSettings.mock.calls[0]?.[0]).toEqual({ screen_required: true });
  await click(
    [...node.querySelectorAll("button")].find((b) => b.textContent === "Trust this source") ?? null,
  );
  expect(routes.trustWithheld.mock.calls[0]?.[0]).toBe("1");
  await click(node.querySelector('[aria-label="Remove example.org"]'));
  expect(routes.removeTrustedSource.mock.calls[0]?.[0]).toBe("example.org");
  act(() => {
    root.unmount();
  });
  client.clear();
});
