/** ProjectFields contains metadata that project tools can change. */
export type ProjectFields = {
  name: string;
  description: string;
  status: "active" | "paused" | "done" | "archived";
  directory: string;
};

/** Project is a durable area of work on the home computer. */
export type Project = ProjectFields & {
  id: string;
  slug: string;
  created_at: string;
  updated_at: string;
};

export type Projects = { projects: Project[] };
export type CreateProject = Pick<Project, "name" | "slug"> & Partial<Omit<ProjectFields, "name">>;
export type ProjectUpdate = { [K in keyof ProjectFields]?: ProjectFields[K] | null };
