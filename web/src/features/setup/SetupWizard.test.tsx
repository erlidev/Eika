import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { connect, disconnect } from "@/api/connection";
import { SetupWizard } from "@/features/setup/SetupWizard";

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean;
}

/** routes answers each API path with a status and a body. */
type Routes = Record<string, [number, unknown]>;

function stubHarness(routes: Routes) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: string) => {
      const path = new URL(input, "http://harness.test").pathname;
      const [status, body] = routes[path] ?? [404, { error: { code: "not_found", message: path } }];
      return Promise.resolve(new Response(JSON.stringify(body), { status }));
    }),
  );
}

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;
  connect({ baseUrl: "", token: "token" });
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
        <SetupWizard />
      </QueryClientProvider>,
    );
    await Promise.resolve();
  });
}

/** settle lets pending fetches and renders finish, until `done` holds or time runs out. */
async function settle(done: () => boolean) {
  for (let i = 0; i < 50 && !done(); i++) {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10));
    });
  }
}

const healthy: Routes = {
  "/api/auth/status": [200, { password_set: true }],
  "/api/providers": [200, { providers: [], kinds: ["openai"] }],
  "/api/models": [200, { models: [] }],
};

describe("SetupWizard", () => {
  it("reports a failed provider list with a Retry instead of starting over", async () => {
    stubHarness({
      ...healthy,
      "/api/providers": [500, { error: { code: "internal", message: "internal error" } }],
    });
    await render();
    await settle(() => container.textContent.includes("Could not load"));

    expect(container.textContent).toContain("Could not load the providers already configured");
    expect(container.textContent).toContain("the harness log names the cause");
    expect(container.querySelector("#provider-name")).toBeNull();

    stubHarness(healthy);
    const retry = [...container.querySelectorAll("button")].find((b) => b.textContent === "Retry");
    expect(retry).toBeDefined();
    await act(async () => {
      retry?.click();
      await Promise.resolve();
    });
    await settle(() => container.querySelector("#provider-name") !== null);
    expect(container.textContent).not.toContain("Could not load");
    expect(container.querySelector("#provider-name")).not.toBeNull();
  });

  it("reports a failed model list", async () => {
    stubHarness({
      ...healthy,
      "/api/models": [503, null],
    });
    await render();
    await settle(() => container.textContent.includes("Could not load"));
    expect(container.textContent).toContain("Could not load the models already configured");
    expect(container.textContent).toContain("HTTP 503");
  });
});
