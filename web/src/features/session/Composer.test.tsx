import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";

import { Composer } from "@/features/session/Composer";
import { useSessionStore } from "@/features/session/store";
import { useTranscriptPreferences } from "@/features/session/preferences";

const post = vi.hoisted(() => vi.fn());
const pointer = vi.hoisted(() => ({ coarse: false }));
vi.mock("@/features/session/queries", () => ({
  useRunStatus: () => ({ data: { active: false } }),
  usePostMessage: () => ({ mutate: post, isPending: false }),
  useCompact: () => ({ mutate: vi.fn(), isPending: false }),
}));
vi.mock("@/lib/useNarrow", () => ({ useMediaQuery: () => pointer.coarse }));

it("does not post when WebKit ends composition before its confirming Enter", () => {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;
  useSessionStore.setState({ draft: "message", draftImages: [] });
  useTranscriptPreferences.setState({ input: "keyboard", composer: "chat" });
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  try {
    act(() => {
      root.render(<Composer sessionId="s" model="" imageInput={false} />);
    });
    const field = container.querySelector("textarea");
    if (!field) throw new Error("No message field");
    act(() => {
      field.dispatchEvent(new CompositionEvent("compositionstart", { bubbles: true }));
      field.dispatchEvent(new CompositionEvent("compositionend", { bubbles: true }));
      field.dispatchEvent(
        new KeyboardEvent("keydown", {
          bubbles: true,
          key: "Enter",
          keyCode: 229,
          isComposing: false,
        }),
      );
    });
    expect(post).not.toHaveBeenCalled();
    act(() => {
      field.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, key: "Enter" }));
    });
    expect(post).toHaveBeenCalledOnce();
  } finally {
    act(() => {
      root.unmount();
    });
    container.remove();
    useSessionStore.setState({ draft: "" });
    useTranscriptPreferences.setState({ input: "auto" });
    post.mockClear();
  }
});

it("keeps the draft when the primary pointer changes", () => {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;
  useSessionStore.setState({ draft: "**kept**", draftImages: [] });
  useTranscriptPreferences.setState({ input: "auto" });
  const container = document.createElement("div");
  const root = createRoot(container);
  const render = () => {
    act(() => {
      root.render(<Composer sessionId="s" model="" imageInput={false} />);
    });
  };
  try {
    pointer.coarse = false;
    render();
    expect(container.querySelector('[aria-label="Message preview"]')).toBeNull();
    pointer.coarse = true;
    render();
    expect(container.querySelector("textarea")?.value).toBe("**kept**");
    expect(container.querySelector('[aria-label="Message preview"] strong')?.textContent).toBe(
      "kept",
    );
    pointer.coarse = false;
    render();
    expect(container.querySelector("textarea")?.value).toBe("**kept**");
  } finally {
    act(() => {
      root.unmount();
    });
    useSessionStore.setState({ draft: "" });
    pointer.coarse = false;
  }
});
