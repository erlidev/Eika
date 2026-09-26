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
};

/** SettingsState is the body of GET and PUT /api/settings. */
export type SettingsState = {
  settings: Settings;
  defaults: SettingsDefaults;
};
