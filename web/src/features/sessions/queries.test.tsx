import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Computer } from "@/api/types";
import { useHomeComputer } from "@/features/sessions/queries";

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean;
}

const routes = vi.hoisted(() => ({ listComputers: vi.fn() }));
vi.mock("@/api/routes", () => routes);

const home: Computer = {
  id: "cx-home",
  name: "home",
  image: "eika-sandbox:latest",
  state: "running",
  sandbox: { limits: {}, egress: { mode: "open", allow: [] } } as unknown as Computer["sandbox"],
  home: true,
  created_at: "2026-10-31T00:00:00Z",
  updated_at: "2026-10-31T00:00:00Z",
};

let container: HTMLDivElement;
let root: Root;
let client: QueryClient;

function Probe() {
  const query = useHomeComputer();
  return <p>{`${query.status}:${query.data?.id ?? "none"}`}</p>;
}

beforeEach(() => {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement("div");
  document.body.append(container);
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  root = createRoot(container);
});

afterEach(() => {
  act(() => {
    root.unmount();
  });
  container.remove();
});

async function mount(computers: Computer[]) {
  routes.listComputers.mockResolvedValue(computers);
  act(() => {
    root.render(
      <QueryClientProvider client={client}>
        <Probe />
      </QueryClientProvider>,
    );
  });
  await vi.waitFor(() => {
    act(() => undefined);
    expect(container.textContent).not.toMatch(/^pending/);
  });
  return container.textContent;
}

describe("useHomeComputer", () => {
  it("finds the home computer", async () => {
    expect(await mount([{ ...home, id: "cx-other", home: false }, home])).toBe("success:cx-home");
  });

  it("is a successful query with no data before the harness has made a home computer", async () => {
    // A query function may not return undefined, which TanStack Query reports
    // as an error; no home computer yet is an answer, not a failure.
    expect(await mount([{ ...home, id: "cx-other", home: false }])).toBe("success:none");
  });
});
