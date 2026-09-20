/** Server state for a workspace's files: directory listings, file contents, and saves. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import { listWorkspaceFiles, readWorkspaceFile, writeWorkspaceFile } from "@/api/routes";
import type { FileContent, FileEntry } from "@/api/types";
import { parentDir } from "@/features/files/editing";

/** useDirectory lists one directory; the tree asks for each as it is expanded. */
export function useDirectory(workspaceId: string, path: string): UseQueryResult<FileEntry[]> {
  return useQuery({
    queryKey: queryKeys.workspaceFiles(workspaceId, path),
    queryFn: ({ signal }) => listWorkspaceFiles(workspaceId, path, signal),
  });
}

/** useFileContent reads the open file; nothing is read while no file is open. */
export function useFileContent(
  workspaceId: string,
  path: string | undefined,
): UseQueryResult<FileContent> {
  return useQuery({
    queryKey: queryKeys.workspaceFile(workspaceId, path ?? ""),
    queryFn: ({ signal }) => readWorkspaceFile(workspaceId, path ?? "", signal),
    enabled: path !== undefined,
  });
}

/** SaveFile is what one save writes. */
export type SaveFile = { path: string; content: string };

/**
 * useSaveFile writes a file, then refreshes what the write changed: the file,
 * its directory's listing (size and time), and the workspace's diff.
 */
export function useSaveFile(workspaceId: string): UseMutationResult<FileEntry, Error, SaveFile> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ path, content }) => writeWorkspaceFile(workspaceId, path, content),
    onSuccess: async (_entry, { path, content }) => {
      client.setQueryData<FileContent>(queryKeys.workspaceFile(workspaceId, path), (old) =>
        old ? { ...old, content, size: new TextEncoder().encode(content).length } : old,
      );
      await Promise.all([
        client.invalidateQueries({ queryKey: queryKeys.workspaceFile(workspaceId, path) }),
        client.invalidateQueries({
          queryKey: queryKeys.workspaceFiles(workspaceId, parentDir(path)),
        }),
        client.invalidateQueries({ queryKey: queryKeys.workspaceDiff(workspaceId) }),
      ]);
    },
  });
}
