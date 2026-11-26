/** Wire types for the settings table. */

import type { SearchLimit } from "@/api/types/search";
import type { SandboxEgress, SandboxLimits } from "@/api/types/computers";

/** Settings is the settings table as one object of arbitrary JSON values. */
export type Settings = Record<string, unknown>;

/** SettingsDefaults are the values the harness uses until the settings name one. */
export type SettingsDefaults = {
  screen_required: boolean;
  screen_threshold: number;
  /** memory_screen_threshold is the score at or over which a memory write goes pending. */
  memory_screen_threshold: number;
  sandbox_image: string;
  /** sandbox_limits and sandbox_egress are what the home computer gets until the user sets others. */
  sandbox_limits: SandboxLimits;
  sandbox_egress: SandboxEgress;
  /** search_order is every web search provider in its default order. */
  search_order: string[];
  /** search_limits is every search quota bucket's default limit. */
  search_limits: Record<string, SearchLimit>;
  /** compaction is the compaction setting's default, with the built-in prompts. */
  compaction: CompactionSettings;
  /** computer_idle_minutes is how long a running computer may go without a run. */
  computer_idle_minutes: number;
  compact_idle_fill: number;
  compact_idle_minutes: number;
  /** subagent_max_running is how many subagents may run at once. */
  subagent_max_running: number;
  /** wakeups_max is the pending wakeup limit per continuing session. */
  wakeups_max: number;
  /** maintenance_idle_seconds, aging_seconds and preempt_running are the inference queue's settings. */
  maintenance_idle_seconds: number;
  aging_seconds: number;
  preempt_running: boolean;
};

/**
 * CompactionSettings is the compaction setting: when a conversation that
 * outgrows its model's window is summarized, and how. A field the stored
 * value leaves out keeps its default, and an empty prompt is the built-in one.
 */
export type CompactionSettings = {
  /** auto compacts before a request that would leave less than reserve_tokens free. */
  auto: boolean;
  /** reserve_tokens is how much of the window stays free. */
  reserve_tokens: number;
  /** keep_recent_tokens is roughly how much of the newest conversation stays verbatim. */
  keep_recent_tokens: number;
  prompts: CompactionPrompts;
};

/** CompactionPrompts are the instructions that ask for a summary. */
export type CompactionPrompts = {
  /** summary asks for the first summary of a conversation. */
  summary: string;
  /** update folds newer messages into the summary already there. */
  update: string;
  /** turn_prefix summarizes the start of a turn too large to keep whole. */
  turn_prefix: string;
};

/** ModelClassName is a role the harness asks a model for. */
export type ModelClassName = "deep" | "main" | "fast" | "decision";

/** modelClassNames lists every class, in the order the settings show them. */
export const modelClassNames: readonly ModelClassName[] = ["deep", "main", "fast", "decision"];

/** ModelClass is one class and the model that serves it, fallbacks applied. */
export type ModelClass = {
  class: ModelClassName;
  /** kind is the kind of model the class takes. */
  kind: "chat" | "decision";
  /** model is the name of the model that serves the class; empty when none does. */
  model: string;
  /** via is the class whose assignment supplied model, when that is not this one, or "default" when the default model did: a fallback. */
  via?: ModelClassName | "default";
  /** provider_id and pool are the endpoint and hardware group of model; classes with one pool contend for its slots. */
  provider_id?: string;
  pool?: string;
};

/** SettingsState is the body of GET and PUT /api/settings. */
export type SettingsState = {
  settings: Settings;
  defaults: SettingsDefaults;
  /** classes is every model class and its model now, fallbacks applied. */
  classes: ModelClass[];
  /** titles_model is the name of the model that makes session titles; empty when none does. */
  titles_model: string;
};

/** GuardHealth is the last observed screening outcome. */
export type GuardHealth = { status: "unknown" | "up" | "down" };
