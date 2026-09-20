/** Server state for projects. Every projects route is reached through here. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import { createProject, deleteProject, listProjects, updateProject } from "@/api/routes";
import type { CreateProject, Project, UpdateProject } from "@/api/types";

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

/** useUpdateProject changes a project's credentials or default branch. */
export function useUpdateProject(): UseMutationResult<
  Project,
  Error,
  { id: string; input: UpdateProject }
> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }) => updateProject(id, input),
    onSuccess: async (project) => {
      client.setQueryData(queryKeys.project(project.id), project);
      await client.invalidateQueries({ queryKey: queryKeys.projects() });
    },
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
