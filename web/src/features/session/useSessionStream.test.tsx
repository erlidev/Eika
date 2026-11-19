import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";

import type { EikaEvent } from "@/api/events";
import { useSessionStore } from "@/features/session/store";
import { items, newTranscript } from "@/features/session/transcript";
import { useSessionStream } from "@/features/session/useSessionStream";

const mocks = vi.hoisted(() => ({
  getSessionEntries: vi.fn(),
  getRunStatus: vi.fn(),
  getSessionOutline: vi.fn(),
  handler: undefined as ((event: EikaEvent) => void) | undefined,
  reopen: undefined as (() => void) | undefined,
}));
vi.mock("@/api/routes", () => ({
  getSessionEntries: mocks.getSessionEntries,
  getRunStatus: mocks.getRunStatus,
  getSessionOutline: mocks.getSessionOutline,
}));
vi.mock("@/api/useStream", () => ({
  useStreamStatus: () => "open",
  useStreamSubscription: (_topic: string, handler: (event: EikaEvent) => void) => {
    mocks.handler = handler;
  },
}));
vi.mock("@/api/stream", () => ({
  eventStream: () => ({
    getStatus: () => "open",
    onStatus: () => () => undefined,
    onReopen: (handler: () => void) => {
      mocks.reopen = handler;
      return () => undefined;
    },
  }),
}));

function View() {
  useSessionStream("s");
  return null;
}

it.each(["reconnect after finish", "finish after reconnect"])(
  "reconciles a missed turn end: %s",
  async (order) => {
    globalThis.IS_REACT_ACT_ENVIRONMENT = true;
    useSessionStore.setState(newTranscript(""));
    let entries: object[] = [];
    let active = true;
    mocks.getSessionEntries.mockImplementation(() => Promise.resolve({ session_id: "s", entries }));
    mocks.getRunStatus.mockImplementation(() => Promise.resolve({ session_id: "s", active }));
    mocks.getSessionOutline.mockImplementation(() =>
      Promise.resolve({ session_id: "s", nodes: [] }),
    );
    const container = document.createElement("div");
    const root = createRoot(container);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const flush = async () => {
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 10));
      });
    };
    try {
      act(() => {
        root.render(
          <QueryClientProvider client={client}>
            <View />
          </QueryClientProvider>,
        );
      });
      await flush();
      act(() => {
        mocks.handler?.({
          type: "turn.start",
          topic: "session:s",
          time: "2026-01-01T00:00:00Z",
          payload: { run_id: "turn", session_id: "s", message: "hello" },
        });
        mocks.handler?.({
          type: "message.delta",
          topic: "session:s",
          time: "2026-01-01T00:00:00Z",
          payload: { run_id: "turn", text: "partial" },
        });
      });
      entries = [
        {
          id: "u",
          seq: 1,
          run_id: "turn",
          kind: "user",
          created_at: "2026-01-01T00:00:00Z",
          message: { role: "user", content: "hello" },
        },
        {
          id: "a",
          seq: 2,
          run_id: "turn",
          kind: "assistant",
          created_at: "2026-01-01T00:00:00Z",
          message: { role: "assistant", content: "complete" },
        },
      ];
      if (order === "finish after reconnect") {
        act(() => {
          mocks.reopen?.();
        });
        await flush();
        expect(useSessionStore.getState().activeTurnId).toBe("turn");
        active = false;
        act(() => {
          mocks.handler?.({
            type: "session.updated",
            topic: "session:s",
            time: "2026-01-01T00:00:00Z",
            payload: { session_id: "s" },
          });
        });
      } else {
        active = false;
        act(() => {
          mocks.reopen?.();
        });
      }
      for (let i = 0; i < 10 && useSessionStore.getState().activeTurnId !== ""; i++) await flush();
      const state = useSessionStore.getState();
      expect(state.activeTurnId).toBe("");
      expect(items(state)).toMatchObject([
        { kind: "user", text: "hello" },
        { kind: "assistant", text: "complete" },
      ]);
    } finally {
      act(() => {
        root.unmount();
      });
      client.clear();
      vi.resetAllMocks();
    }
  },
);
