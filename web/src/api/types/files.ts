/** Wire types for a computer's files. */

/** FileEntry is one file or directory of a computer listing. */
export type FileEntry = {
  name: string;
  /** path is relative to the computer root. */
  path: string;
  size: number;
  mode: number;
  mod_time: string;
  is_dir: boolean;
};

/** FileContent is the body of GET /api/computers/{id}/file. */
export type FileContent = {
  path: string;
  size: number;
  /** binary says the file holds NUL bytes; content is then empty. */
  binary: boolean;
  /** too_large says the file is over the 2 MiB limit; content is then empty. */
  too_large: boolean;
  content: string;
};
