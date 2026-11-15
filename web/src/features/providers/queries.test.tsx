import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { isSessionConfiguration, queryKeys } from "@/api/keys";
import { useDeleteModel } from "@/features/providers/queries";

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean;
}

const routes = vi.hoisted(() => ({ deleteModel: vi.fn() }));
vi.mock("@/api/routes", () => routes);

let container: HTMLDivElement;

beforeEach(() => {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement("div");
  document.body.append(container);
});

afterEach(() => {
  container.remove();
});

describe("isSessionConfiguration", () => {
  it("matches the configuration of any session, and nothing else", () => {
    expect(isSessionConfiguration(queryKeys.sessionConfiguration("a"))).toBe(true);
    expect(isSessionConfiguration(queryKeys.sessionConfiguration("b"))).toBe(true);
    expect(isSessionConfiguration(queryKeys.session("a"))).toBe(false);
    expect(isSessionConfiguration(queryKeys.sessionContext("a"))).toBe(false);
    expect(isSessionConfiguration(queryKeys.models())).toBe(false);
  });
});

describe("a model change", () => {
  it("makes every session's cached configuration stale, since it names the resolved model", async () => {
    routes.deleteModel.mockResolvedValue(undefined);
    const client = new QueryClient();
    client.setQueryData(queryKeys.sessionConfiguration("home"), { resolved: { model: "gpt-5" } });
    client.setQueryData(queryKeys.sessionConfiguration("other"), {});
    client.setQueryData(queryKeys.session("home"), {});
    let remove: (name: string) => Promise<void> = () => Promise.resolve();
    function Probe() {
      const mutation = useDeleteModel();
      remove = (name) => mutation.mutateAsync(name);
      return null;
    }
    const root = createRoot(container);
    act(() => {
      root.render(
        <QueryClientProvider client={client}>
          <Probe />
        </QueryClientProvider>,
      );
    });
    await act(async () => {
      await remove("gpt-5");
    });
    const stale = (key: readonly unknown[]) => client.getQueryState(key)?.isInvalidated;
    expect(stale(queryKeys.sessionConfiguration("home"))).toBe(true);
    expect(stale(queryKeys.sessionConfiguration("other"))).toBe(true);
    expect(stale(queryKeys.session("home"))).toBe(false);
    act(() => {
      root.unmount();
    });
  });
});
