/** Server state for the settings table and the system check. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import { getSettings, getSystem, putSettings } from "@/api/routes";
import type { Settings, SettingsState, SystemStatus } from "@/api/types";

/** The settings keys the harness reads itself. Every other key is the UI's own. */
export const settingKeys = {
  defaultModel: "default_model",
  sandboxImage: "sandbox_image",
  subagentMaxDepth: "subagent_max_depth",
  subagentMaxChildren: "subagent_max_children",
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
      // The default model, the sandbox image, and the search order and
      // quotas are reported elsewhere too.
      await Promise.all([
        client.invalidateQueries({ queryKey: queryKeys.models() }),
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
