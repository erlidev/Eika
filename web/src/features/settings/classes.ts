/** What the model classes setting says about each class, from the harness's resolved view. */

import type { ModelClass, ModelClassName } from "@/api/types";

/** classInfo describes each class for the settings: its name and what it is for. */
export const classInfo: Record<ModelClassName, { label: string; hint: string }> = {
  deep: {
    label: "Deep",
    hint: "Hard reasoning, and anything an agent definition marks deep. May be a cloud model. Falls back to main.",
  },
  main: {
    label: "Main",
    hint: "The main agent and most tasks. Falls back to the default model.",
  },
  fast: {
    label: "Fast",
    hint: "Session titles and other small tasks. Falls back to main, so a missing fast model does not turn titles off.",
  },
  decision: {
    label: "Decision",
    hint: "A decision model answers typed questions with calibrated probabilities instead of writing text. Nothing asks it anything yet.",
  },
};

/**
 * sharedWith lists the other classes served from the same pool as c: the
 * classes that contend with it for the pool's slots. A class with no model
 * shares nothing.
 */
export function sharedWith(classes: readonly ModelClass[], c: ModelClass): ModelClassName[] {
  const pool = c.pool;
  if (c.model === "" || pool === undefined || pool === "") return [];
  return classes.filter((o) => o.class !== c.class && o.pool === pool).map((o) => o.class);
}

/** joinNames writes a list of names as prose: "a", "a and b", "a, b and c". */
export function joinNames(names: readonly string[]): string {
  if (names.length <= 1) return names.join("");
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1] ?? ""}`;
}

/**
 * titlesStatus says who makes session titles, and why nobody does when
 * nobody does: the fast class falls back to main and main to the default
 * model, except onto a hosted deep model.
 */
export function titlesStatus(classes: readonly ModelClass[], titlesModel: string): string {
  if (titlesModel !== "") return `Session titles are made by ${titlesModel}.`;
  const fast = classes.find((c) => c.class === "fast");
  if (fast !== undefined && fast.model !== "") {
    return `Session titles are skipped: fast falls back onto ${fast.model}, the deep model, which is a hosted API. Choose a fast model to turn them on.`;
  }
  return "Session titles are off until there is a chat model to make them.";
}
