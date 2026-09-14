/** Server state for the settings table and the configured models. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import { getModels, getSettings, putSettings } from "@/api/routes";
import type { Models, Settings } from "@/api/types";

/** defaultModelKey is the one setting the harness reads itself. */
export const defaultModelKey = "default_model";

/** useSettings reads the settings table as one object. */
export function useSettings(): UseQueryResult<Settings> {
  return useQuery({
    queryKey: queryKeys.settings(),
    queryFn: ({ signal }) => getSettings(signal),
  });
}

/** useModels lists the configured models and the chosen default. */
export function useModels(): UseQueryResult<Models> {
  return useQuery({
    queryKey: queryKeys.models(),
    queryFn: ({ signal }) => getModels(signal),
  });
}

/** useSaveSettings writes the named keys and leaves the rest alone. */
export function useSaveSettings(): UseMutationResult<Settings, Error, Settings> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: putSettings,
    onSuccess: async (settings) => {
      client.setQueryData(queryKeys.settings(), settings);
      // `default_model` is reported by /api/models as well.
      await client.invalidateQueries({ queryKey: queryKeys.models() });
    },
  });
}

/** defaultModelOf reads the default model out of the settings table. */
export function defaultModelOf(settings: Settings | undefined): string {
  const value = settings?.[defaultModelKey];
  return typeof value === "string" ? value : "";
}
