// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";

import { MarkdownField } from "@/features/session/MarkdownField";

it("sends with desktop keys, but leaves touch and composition keys to the browser", () => {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  const send = vi.fn();
  const render = (touch: boolean, mode: "chat" | "markdown") => {
    act(() => {
      root.render(<MarkdownField value="hello" mode={mode} touch={touch} onSend={send} />);
    });
  };
  const key = (options: KeyboardEventInit) => {
    const event = new KeyboardEvent("keydown", {
      bubbles: true,
      cancelable: true,
      key: "Enter",
      ...options,
    });
    act(() => {
      container.querySelector("textarea")?.dispatchEvent(event);
    });
    return event.defaultPrevented;
  };
  render(false, "chat");
  expect(key({ isComposing: true })).toBe(false);
  expect(key({})).toBe(true);
  expect(send).toHaveBeenCalledTimes(1);
  render(false, "markdown");
  expect(key({ ctrlKey: true })).toBe(true);
  expect(send).toHaveBeenCalledTimes(2);
  render(true, "chat");
  expect(key({})).toBe(false);
  expect(key({ ctrlKey: true })).toBe(false);
  expect(send).toHaveBeenCalledTimes(2);
  act(() => {
    root.unmount();
  });
  container.remove();
});
