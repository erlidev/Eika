import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ConfigurationError } from "@/features/session/ConfigurationError";

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean;
}

let container: HTMLDivElement;

beforeEach(() => {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement("div");
  document.body.append(container);
});

afterEach(() => {
  container.remove();
});

describe("ConfigurationError", () => {
  it("says what failed and retries on Retry", () => {
    const retry = vi.fn();
    const root = createRoot(container);
    act(() => {
      root.render(<ConfigurationError error={new Error("boom")} retry={retry} retrying={false} />);
    });
    expect(container.querySelector('[role="alert"]')?.textContent).toContain(
      "Could not load the session's model",
    );
    const button = container.querySelector("button");
    expect(button?.textContent).toBe("Retry");
    act(() => {
      button?.click();
    });
    expect(retry).toHaveBeenCalledTimes(1);
    act(() => {
      root.unmount();
    });
  });

  it("disables Retry while it retries", () => {
    const root = createRoot(container);
    act(() => {
      root.render(<ConfigurationError error={new Error("boom")} retry={vi.fn()} retrying />);
    });
    expect(container.querySelector("button")?.disabled).toBe(true);
    act(() => {
      root.unmount();
    });
  });
});
