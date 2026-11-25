/** Server state for the Skills page: the list, one skill and the install. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import { deleteSkill, getSkill, installSkill, listSkills, setSkillEnabled } from "@/api/routes";
import type { Skill, SkillInstall, SkillItem } from "@/api/types";

/** useSkills lists every installed skill, by name. */
export function useSkills(): UseQueryResult<SkillItem[]> {
  return useQuery({
    queryKey: [...queryKeys.skills(), "list"] as const,
    queryFn: ({ signal }) => listSkills(signal),
    select: (data) => data.skills,
  });
}

/** useSkill reads one skill in full. */
export function useSkill(name: string): UseQueryResult<Skill> {
  return useQuery({
    queryKey: queryKeys.skill(name),
    queryFn: ({ signal }) => getSkill(name, signal),
  });
}

/** useInstallSkill uploads a zip; the new skill arrives disabled. */
export function useInstallSkill(): UseMutationResult<SkillInstall, Error, File> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (file: File) => installSkill(file),
    onSuccess: () => client.invalidateQueries({ queryKey: queryKeys.skills() }),
  });
}

/** useSetSkillEnabled changes the skill state and refreshes the catalog. */
export function useSetSkillEnabled(name: string): UseMutationResult<Skill, Error, boolean> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (enabled: boolean) => setSkillEnabled(name, enabled),
    onSuccess: () => client.invalidateQueries({ queryKey: queryKeys.skills() }),
  });
}

/** useDeleteSkill removes a skill and refreshes the catalog. */
export function useDeleteSkill(name: string): UseMutationResult<void, Error, void> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: () => deleteSkill(name),
    onSuccess: () => client.invalidateQueries({ queryKey: queryKeys.skills() }),
  });
}
