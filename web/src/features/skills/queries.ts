/** Server state for the Skills page: the list, one skill and the install. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import { getSkill, installSkill, listSkills } from "@/api/routes";
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
