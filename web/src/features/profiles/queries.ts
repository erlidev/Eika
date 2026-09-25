/** Server state for the profiles and for what a session sets over its profile. */

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { QueryClient, UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import {
  createProfile,
  deleteProfile,
  getInheritedProfile,
  getSessionConfiguration,
  listProfiles,
  setSessionOverrides,
  setSessionProfile,
  setSessionTools,
  updateProfile,
} from "@/api/routes";
import type { ConfigurationDraft } from "@/api/routes";
import type {
  Configuration,
  Profile,
  ProfileInput,
  Profiles,
  ProfileSettings,
  SessionConfiguration,
} from "@/api/types";

/**
 * refreshSessions invalidates what a profile or an override changes beyond
 * itself: every session's configuration, its tools, and its next request.
 */
async function refreshSessions(client: QueryClient): Promise<void> {
  await Promise.all([
    client.invalidateQueries({ queryKey: queryKeys.profiles() }),
    client.invalidateQueries({ queryKey: ["session"] }),
    client.invalidateQueries({ queryKey: ["sessions"] }),
  ]);
}

/** useProfiles lists the profiles, the default one, and the built-in prompts. */
export function useProfiles(): UseQueryResult<Profiles> {
  return useQuery({
    queryKey: queryKeys.profiles(),
    queryFn: ({ signal }) => listProfiles(signal),
  });
}

/** useCreateProfile adds a profile. */
export function useCreateProfile(): UseMutationResult<Profile, Error, ProfileInput> {
  const client = useQueryClient();
  return useMutation({ mutationFn: createProfile, onSuccess: () => refreshSessions(client) });
}

/** useUpdateProfile replaces a profile. */
export function useUpdateProfile(): UseMutationResult<
  Profile,
  Error,
  { id: string; input: ProfileInput }
> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }) => updateProfile(id, input),
    onSuccess: () => refreshSessions(client),
  });
}

/** useDeleteProfile removes a profile. */
export function useDeleteProfile(): UseMutationResult<void, Error, string> {
  const client = useQueryClient();
  return useMutation({ mutationFn: deleteProfile, onSuccess: () => refreshSessions(client) });
}

/** useSessionConfiguration reads what a session sets itself and what it resolves to. */
export function useSessionConfiguration(
  sessionId: string | undefined,
): UseQueryResult<SessionConfiguration> {
  return useQuery({
    queryKey: queryKeys.sessionConfiguration(sessionId ?? ""),
    queryFn: ({ signal }) => getSessionConfiguration(sessionId ?? "", {}, signal),
    enabled: sessionId !== undefined,
  });
}

/**
 * useDraftConfiguration reads what a session's overrides fall through to
 * with the profile and model its editor has chosen and not saved. It asks
 * only while the draft differs from what is saved, and keeps the last
 * answer while the next loads so the editor does not flicker.
 */
export function useDraftConfiguration(
  sessionId: string,
  draft: ConfigurationDraft | undefined,
): UseQueryResult<SessionConfiguration> {
  return useQuery({
    queryKey: queryKeys.sessionConfiguration(sessionId, draft ?? {}),
    queryFn: ({ signal }) => getSessionConfiguration(sessionId, draft, signal),
    enabled: draft !== undefined,
    placeholderData: keepPreviousData,
  });
}

/**
 * useInheritedProfile reads what a profile choosing a model ("" for none)
 * falls through to, for an editor whose model is not saved yet.
 */
export function useInheritedProfile(
  modelId: string,
  enabled: boolean,
): UseQueryResult<Configuration> {
  return useQuery({
    queryKey: queryKeys.profileInherited(modelId),
    queryFn: ({ signal }) => getInheritedProfile(modelId, signal),
    enabled,
    placeholderData: keepPreviousData,
  });
}

/** SessionSettings is what the session editor saves: its profile, overrides, and tools. */
export type SessionSettings = {
  profileId: string;
  overrides: ProfileSettings;
  /** tools is the session's own tool choice; null goes back to the profile's. */
  tools: string[] | null;
};

/**
 * useSaveSessionSettings writes a session's profile, its overrides, and its
 * tool choice, in that order, and answers with the configuration they make.
 */
export function useSaveSessionSettings(
  sessionId: string,
): UseMutationResult<SessionConfiguration, Error, SessionSettings> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async ({ profileId, overrides, tools }) => {
      await setSessionProfile(sessionId, profileId);
      await setSessionTools(sessionId, tools);
      return setSessionOverrides(sessionId, overrides);
    },
    onSuccess: async (config) => {
      client.setQueryData(queryKeys.sessionConfiguration(sessionId), config);
      await refreshSessions(client);
    },
  });
}
