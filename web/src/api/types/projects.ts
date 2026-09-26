/** Wire types for projects. */

/** Project is a git repository Eika knows. */
export type Project = {
  id: string;
  name: string;
  kind: ProjectKind;
  remote_url?: string;
  /** remote_username is the user the hub authenticates to the upstream as. */
  remote_username?: string;
  /** remote_password_set says a password is stored; the password never leaves the harness. */
  remote_password_set?: boolean;
  host_path?: string;
  default_branch: string;
  created_at: string;
};

/** ProjectKind says where a project's code comes from. */
export type ProjectKind = "remote" | "local";

/** CreateProject is the body of POST /api/projects. */
export type CreateProject = {
  name: string;
  kind: ProjectKind;
  remote_url?: string;
  remote_username?: string;
  remote_password?: string;
  host_path?: string;
  default_branch?: string;
};

/** UpdateProject is the body of PATCH /api/projects/{id}; an absent field is left alone. */
export type UpdateProject = {
  remote_username?: string;
  /** remote_password replaces the stored one; "" removes the credentials. */
  remote_password?: string;
  default_branch?: string;
};
