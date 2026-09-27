/**
 * How the transcript renders and the composer behaves, remembered by this
 * browser. These are not harness settings: two people in the same session
 * can want different things from it, and the choice has to survive a reload,
 * so it is localStorage behind a small store.
 */

import { create } from "zustand";

import { readPersistedString, writePersisted } from "@/lib/persisted";

/**
 * ReasoningDisplay is how much of the model's thinking the transcript shows.
 * `compact` keeps it to one line that can be opened, which is what a reader
 * following an answer wants; `expanded` opens every block, which is what
 * someone debugging a prompt wants.
 */
export type ReasoningDisplay = "compact" | "expanded";

/**
 * ComposerMode is what the message box's keys do. `chat` is a chat app's:
 * Enter sends. `markdown` is an editor's: Enter starts a line, carrying a
 * list or quotation on, Tab nests a list item, a code fence closes itself,
 * and Ctrl+Enter sends.
 */
export type ComposerMode = "chat" | "markdown";

const reasoningKey = "eika.transcript.reasoning";
const speedKey = "eika.transcript.speed";
const composerKey = "eika.composer.mode";

const displays: readonly ReasoningDisplay[] = ["compact", "expanded"];

/** storedReasoning reads the remembered choice, defaulting to compact. */
function storedReasoning(): ReasoningDisplay {
  const value = readPersistedString(reasoningKey, "compact");
  return displays.includes(value as ReasoningDisplay) ? (value as ReasoningDisplay) : "compact";
}

/** storedSpeed reads whether the transcript states generation speeds. */
function storedSpeed(): boolean {
  return readPersistedString(speedKey, "off") === "on";
}

/** storedComposer reads the remembered composer mode, defaulting to chat. */
function storedComposer(): ComposerMode {
  return readPersistedString(composerKey, "chat") === "markdown" ? "markdown" : "chat";
}

type TranscriptPreferences = {
  reasoning: ReasoningDisplay;
  setReasoning: (display: ReasoningDisplay) => void;
  /**
   * speed is whether each answer says how fast it was produced. It is off by
   * default: a rate is of interest while choosing an endpoint or a quant, and
   * a line under every answer the rest of the time.
   */
  speed: boolean;
  setSpeed: (on: boolean) => void;
  composer: ComposerMode;
  setComposer: (mode: ComposerMode) => void;
};

/** useTranscriptPreferences is how the session view renders and behaves here. */
export const useTranscriptPreferences = create<TranscriptPreferences>((set) => ({
  reasoning: storedReasoning(),
  setReasoning: (reasoning) => {
    writePersisted(reasoningKey, reasoning);
    set({ reasoning });
  },
  speed: storedSpeed(),
  setSpeed: (speed) => {
    writePersisted(speedKey, speed ? "on" : "off");
    set({ speed });
  },
  composer: storedComposer(),
  setComposer: (composer) => {
    writePersisted(composerKey, composer);
    set({ composer });
  },
}));
