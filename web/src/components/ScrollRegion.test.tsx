import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it } from "vitest";

import { ScrollRegion } from "@/components/ScrollRegion";

it("moves a focused scroll region to the end and back to the start", () => {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;
  const container = document.createElement("div");
  const root = createRoot(container);
  try {
    act(() => {
      root.render(<ScrollRegion label="Long text">Long text</ScrollRegion>);
    });
    const region = container.querySelector<HTMLElement>('[role="region"]');
    if (!region) throw new Error("Scroll region not found");
    Object.defineProperty(region, "scrollHeight", { value: 1000 });
    act(() => {
      region.dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true }));
    });
    expect(region.scrollTop).toBe(1000);
    act(() => {
      region.dispatchEvent(new KeyboardEvent("keydown", { key: "Home", bubbles: true }));
    });
    expect(region.scrollTop).toBe(0);
  } finally {
    act(() => {
      root.unmount();
    });
  }
});
