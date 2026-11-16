/**
 * The streaming state of the session that is open. It is a thin Zustand shell
 * around the pure reducer in `transcript.ts`: the store owns which session is
 * open and nothing else, so the folding rules stay testable on their own.
 */

import { create } from "zustand";

import type { EikaEvent } from "@/api/events";
import type { DraftImage } from "@/features/session/attachments";
import { applyEvent, newTranscript } from "@/features/session/transcript";
import type { TranscriptState } from "@/features/session/transcript";

/** Composing is what a session's composer holds: its model choice, its draft and the draft's images. */
type Composing = { model: string; draft: string; draftImages: DraftImage[] };

const noComposing: Composing = { model: "", draft: "", draftImages: [] };

/** SessionStore is the open session's transcript and the ways it changes. */
export type SessionStore = TranscriptState & {
  /**
   * model is the model this session's next run uses, empty for the default.
   * It is here rather than in the session view because `open` parks it with
   * the draft when another session opens, and restores it on return.
   */
  model: string;
  /**
   * draft is the text in the composer. It is in the store rather than in the
   * composer because rewinding to a message puts that message back in the box
   * for editing, and the two are not each other's parents.
   */
  draft: string;
  /** draftImages are the images attached in the composer, kept beside draft for the same reason. */
  draftImages: DraftImage[];
  /**
   * parked is what the composer of each session that is not open holds, so
   * that watching another session (a subagent in Activity) and coming back
   * leaves an unsent message and its images where they were.
   */
  parked: Record<string, Composing>;
  /**
   * open points the store at a session, discarding the previous one's
   * transcript and parking its composer state for when it opens again.
   */
  open: (sessionId: string) => void;
  /** edit replaces the composer's text. */
  edit: (draft: string) => void;
  /** editImages replaces the composer's images. */
  editImages: (images: DraftImage[]) => void;
  /**
   * rewound discards the transcript and asks for the session's path again.
   * Moving the head makes the entries after it no longer part of the
   * conversation, and a replay only ever adds: without this the abandoned
   * branch stays on screen and the next turn reads as continuing it.
   */
  rewound: () => void;
  /** chooseModel points this session's next run at a model. */
  chooseModel: (model: string) => void;
  /** apply folds one stream event in. */
  apply: (e: EikaEvent) => void;
  /** replayRequested records that the replay the state asked for is on its way. */
  replayRequested: () => void;
  /** replayNeeded asks for a catch-up, for a reconnect that lost live events. */
  replayNeeded: () => void;
  /** dismissQuestion drops a question the user has just answered. */
  dismissQuestion: (questionId: string) => void;
  /** dismissElicitation drops a request for input the user has just answered. */
  dismissElicitation: (elicitationId: string) => void;
};

export const useSessionStore = create<SessionStore>((set) => ({
  ...newTranscript(""),
  model: "",
  draft: "",
  draftImages: [],
  parked: {},
  open: (sessionId) => {
    set((state) => {
      if (state.sessionId === sessionId) return state;
      const { [sessionId]: restored, ...others } = state.parked;
      const leaving: Composing = {
        model: state.model,
        draft: state.draft,
        draftImages: state.draftImages,
      };
      const empty =
        leaving.model === "" && leaving.draft === "" && leaving.draftImages.length === 0;
      return {
        ...newTranscript(sessionId),
        ...(restored ?? noComposing),
        parked:
          state.sessionId === "" || empty ? others : { ...others, [state.sessionId]: leaving },
      };
    });
  },
  edit: (draft) => {
    set({ draft });
  },
  editImages: (draftImages) => {
    set({ draftImages });
  },
  rewound: () => {
    set((state) => ({
      ...newTranscript(state.sessionId),
      model: state.model,
      draft: state.draft,
      draftImages: state.draftImages,
      needsReplay: true,
    }));
  },
  chooseModel: (model) => {
    set({ model });
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
  dismissElicitation: (elicitationId) => {
    set((state) => ({
      elicitations: state.elicitations.filter((x) => x.id !== elicitationId),
    }));
  },
}));
