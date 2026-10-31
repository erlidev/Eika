/** Wire types for a workspace's files. */

/** FileEntry is one file or directory of a workspace listing. */
export type FileEntry = {
  name: string;
  /** path is relative to the workspace root. */
  path: string;
  size: number;
  mode: number;
  mod_time: string;
  is_dir: boolean;
};

/** FileContent is the body of GET /api/workspaces/{id}/file. */
export type FileContent = {
  path: string;
  size: number;
  /** binary says the file holds NUL bytes; content is then empty. */
  binary: boolean;
  /** too_large says the file is over the 2 MiB limit; content is then empty. */
  too_large: boolean;
  content: string;
};
