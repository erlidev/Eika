/** Wire types for the settings table. */

import type { SearchLimit } from "@/api/types/search";
import type { SandboxEgress, SandboxLimits } from "@/api/types/workspaces";

/** Settings is the settings table as one object of arbitrary JSON values. */
export type Settings = Record<string, unknown>;

/** SettingsDefaults are the values the harness uses until the settings name one. */
export type SettingsDefaults = {
  sandbox_image: string;
  subagent_max_depth: number;
  subagent_max_children: number;
  /** sandbox_limits and sandbox_egress are what a new workspace gets. */
  sandbox_limits: SandboxLimits;
  sandbox_egress: SandboxEgress;
  /** search_order is every web search provider in its default order. */
  search_order: string[];
  /** search_limits is every search quota bucket's default limit. */
  search_limits: Record<string, SearchLimit>;
  /** compaction is the compaction setting's default, with the built-in prompts. */
  compaction: CompactionSettings;
  /** workspace_idle_minutes is how long a running workspace may go without a run. */
  workspace_idle_minutes: number;
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

/** SettingsState is the body of GET and PUT /api/settings. */
export type SettingsState = {
  settings: Settings;
  defaults: SettingsDefaults;
};
