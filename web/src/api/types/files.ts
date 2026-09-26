/** Wire types for a workspace's files, diff, commit, and push. */

/** WorkspaceDiff is the body of GET /api/workspaces/{id}/diff. */
export type WorkspaceDiff = {
  workspace_id: string;
  base_commit?: string;
  diff: string;
  status: string;
};

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

/** CommitRequest is the body of POST /api/workspaces/{id}/commit. */
export type CommitRequest = {
  message: string;
  /** paths limits the commit to these files; absent commits everything. */
  paths?: string[];
};

/** CommitResult is what a commit made. */
export type CommitResult = {
  commit: string;
};

/** PushRequest is the body of POST /api/workspaces/{id}/push. */
export type PushRequest = {
  /** upstream also pushes the branch from the hub to the project's remote. */
  upstream?: boolean;
};

/** PushResult is where a push put the workspace's branch. */
export type PushResult = {
  branch: string;
  commit: string;
  upstream_pushed: boolean;
};
