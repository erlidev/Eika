/**
 * The streaming state of the session that is open. It is a thin Zustand shell
 * around the pure reducer in `transcript.ts`: the store owns which session is
 * open and nothing else, so the folding rules stay testable on their own.
 */

import { create } from "zustand";

import type { EikaEvent } from "@/api/events";
import { applyEvent, newTranscript } from "@/features/session/transcript";
import type { TranscriptState } from "@/features/session/transcript";

/** SessionStore is the open session's transcript and the ways it changes. */
export type SessionStore = TranscriptState & {
  /** open points the store at a session, discarding the previous one. */
  open: (sessionId: string) => void;
  /** apply folds one stream event in. */
  apply: (e: EikaEvent) => void;
  /** replayRequested records that the replay the state asked for is on its way. */
  replayRequested: () => void;
  /** replayNeeded asks for a catch-up, for a reconnect that lost live events. */
  replayNeeded: () => void;
  /** dismissQuestion drops a question the user has just answered. */
  dismissQuestion: (questionId: string) => void;
};

export const useSessionStore = create<SessionStore>((set) => ({
  ...newTranscript(""),
  open: (sessionId) => {
    set((state) => (state.sessionId === sessionId ? state : newTranscript(sessionId)));
  },
  apply: (e) => {
    set((state) => applyEvent(state, e));
  },
  replayRequested: () => {
    set({ needsReplay: false });
  },
  replayNeeded: () => {
    set({ needsReplay: true });
  },
  dismissQuestion: (questionId) => {
    set((state) => ({ questions: state.questions.filter((q) => q.id !== questionId) }));
  },
}));
