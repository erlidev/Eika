/** Server state for the settings table and the system check. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import { getSettings, getSystem, putSettings } from "@/api/routes";
import type {
  SandboxEgress,
  SandboxLimits,
  Settings,
  SettingsState,
  SystemStatus,
} from "@/api/types";

/** The settings keys the harness reads itself. Every other key is the UI's own. */
export const settingKeys = {
  defaultModel: "default_model",
  defaultProfile: "default_profile",
  sandboxImage: "sandbox_image",
  subagentMaxDepth: "subagent_max_depth",
  subagentMaxChildren: "subagent_max_children",
  sandboxLimits: "sandbox_limits",
  sandboxEgress: "sandbox_egress",
  setupComplete: "setup_complete",
} as const;

/** useSettings reads the settings table and the harness's defaults. */
export function useSettings(): UseQueryResult<SettingsState> {
  return useQuery({
    queryKey: queryKeys.settings(),
    queryFn: ({ signal }) => getSettings(signal),
  });
}

/** useSaveSettings writes the named keys and leaves the rest alone. */
export function useSaveSettings(): UseMutationResult<SettingsState, Error, Settings> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: putSettings,
    onSuccess: async (state) => {
      client.setQueryData(queryKeys.settings(), state);
      // The default model and profile, the sandbox image, and the search
      // order and quotas are reported elsewhere too.
      await Promise.all([
        client.invalidateQueries({ queryKey: queryKeys.models() }),
        client.invalidateQueries({ queryKey: queryKeys.profiles() }),
        client.invalidateQueries({ queryKey: ["session"] }),
        client.invalidateQueries({ queryKey: queryKeys.system() }),
        client.invalidateQueries({ queryKey: queryKeys.searchStatus() }),
      ]);
    },
  });
}

/** useSystem reports whether Docker and the sandbox image are ready. */
export function useSystem(): UseQueryResult<SystemStatus> {
  return useQuery({
    queryKey: queryKeys.system(),
    queryFn: ({ signal }) => getSystem(signal),
  });
}

/** settingString reads a string setting, "" when it is absent or not a string. */
export function settingString(state: SettingsState | undefined, key: string): string {
  const value = state?.settings[key];
  return typeof value === "string" ? value : "";
}

/** settingNumber reads a numeric setting, undefined when it is absent. */
export function settingNumber(state: SettingsState | undefined, key: string): number | undefined {
  const value = state?.settings[key];
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

/** setupComplete reports whether the user finished or skipped the guided setup. */
export function setupComplete(state: SettingsState | undefined): boolean {
  return state?.settings[settingKeys.setupComplete] === true;
}

/** SandboxDefaults are the limits and network a new workspace gets. */
export type SandboxDefaults = { limits: SandboxLimits; egress: SandboxEgress };

/**
 * sandboxDefaults is what the settings store, else the harness's own
 * defaults. The table holds any JSON, so a stored value is read only when
 * it has the shape the harness validated it to.
 */
export function sandboxDefaults(state: SettingsState): SandboxDefaults {
  const limits = state.settings[settingKeys.sandboxLimits];
  const egress = state.settings[settingKeys.sandboxEgress];
  return {
    limits: isLimits(limits) ? limits : state.defaults.sandbox_limits,
    egress: isEgress(egress) ? egress : state.defaults.sandbox_egress,
  };
}

function isLimits(value: unknown): value is SandboxLimits {
  if (typeof value !== "object" || value === null) return false;
  const v = value as Record<string, unknown>;
  return ["cpus", "memory_mb", "pids"].every((k) => typeof v[k] === "number");
}

function isEgress(value: unknown): value is SandboxEgress {
  if (typeof value !== "object" || value === null) return false;
  const v = value as Record<string, unknown>;
  return (
    (v.mode === "open" || v.mode === "allowlist" || v.mode === "none") &&
    (v.allow === null || (Array.isArray(v.allow) && v.allow.every((h) => typeof h === "string")))
  );
}
