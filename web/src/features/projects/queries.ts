/** Server state for projects. Every projects route is reached through here. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import { createProject, deleteProject, listProjects } from "@/api/routes";
import type { CreateProject, Project } from "@/api/types";

/** useProjects lists every project the harness knows. */
export function useProjects(): UseQueryResult<Project[]> {
  return useQuery({
    queryKey: queryKeys.projects(),
    queryFn: ({ signal }) => listProjects(signal),
  });
}

/** useCreateProject registers a repository and refreshes the list. */
export function useCreateProject(): UseMutationResult<Project, Error, CreateProject> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: createProject,
    onSuccess: () => client.invalidateQueries({ queryKey: queryKeys.projects() }),
  });
}

/**
 * useDeleteProject destroys a project. Its workspaces and sessions go with it,
 * so both lists are refreshed.
 */
export function useDeleteProject(): UseMutationResult<void, Error, string> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: deleteProject,
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: queryKeys.projects() });
      await client.invalidateQueries({ queryKey: ["workspaces"] });
      await client.invalidateQueries({ queryKey: ["sessions"] });
    },
  });
}
