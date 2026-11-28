import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { SecuritySettings } from "@/features/settings/SecuritySettings";

const routes = vi.hoisted(() => {
  const sentry = {
    watch_threshold: 0.5,
    sensitive_list: [] as string[],
    sink_limits: { upload_bytes: 1, upload_ratio_bytes: 1, upload_ratio: 1, fan_out_urls: 1 },
    tripwires: { followed: false, blocked_limit: 10 },
  };
  return {
    getGuard: vi.fn().mockResolvedValue({ status: "down" }),
    getSettings: vi.fn().mockResolvedValue({
      settings: {},
      defaults: { screen_required: false, screen_threshold: 0.9, ...sentry },
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
    getBlockedHosts: vi.fn().mockResolvedValue({ hosts: ["blocked.example"] }),
    addBlockedHost: vi.fn().mockResolvedValue(undefined),
    removeBlockedHost: vi.fn().mockResolvedValue(undefined),
    getTrustedSources: vi.fn().mockResolvedValue({ hosts: ["example.org"] }),
    putSettings: vi.fn().mockResolvedValue({
      settings: { screen_required: true },
      defaults: { screen_required: false, screen_threshold: 0.9, ...sentry },
    }),
    addTrustedSource: vi.fn().mockResolvedValue(undefined),
    removeTrustedSource: vi.fn().mockResolvedValue(undefined),
    trustWithheld: vi.fn().mockResolvedValue(undefined),
    getTripwireCounts: vi.fn().mockResolvedValue({ counts: [] }),
  };
});
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
  await click(host.closest("form")?.querySelector("button") ?? null);
  expect(routes.addTrustedSource.mock.calls[0]?.[0]).toBe("new.example");
  const blocked = node.querySelector<HTMLInputElement>("#blocked-host");
  if (!blocked) throw new Error("missing blocked host");
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(
      blocked,
      "new.blocked",
    );
    blocked.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await click(blocked.closest("form")?.querySelector("button") ?? null);
  expect(routes.addBlockedHost.mock.calls[0]?.[0]).toBe("new.blocked");
  await click(node.querySelector('[aria-label="Remove blocked.example"]'));
  expect(routes.removeBlockedHost.mock.calls[0]?.[0]).toBe("blocked.example");
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
