/** Wire types for the user's skills API; see docs/api/http.md, "Skills". */

/** SkillFile is one file of an installed skill. */
export type SkillFile = {
  path: string;
  size: number;
};

/** SkillScreenSpan is one screened window at or over the threshold; offsets are bytes. */
export type SkillScreenSpan = {
  field: "description" | "body";
  start: number;
  end: number;
  score: number;
  text: string;
};

/** SkillScreen is the install screen's finding. unscanned means the guard did not answer. */
export type SkillScreen = {
  spans: SkillScreenSpan[];
  max_score: number;
  unscanned: boolean;
};

/** SkillItem is one skill in the list, without its body or file list. */
export type SkillItem = {
  name: string;
  description: string;
  enabled: boolean;
  /** broken is empty, or why the skill cannot be loaded or enabled. */
  broken: string;
  installed_at: string;
  sha: string;
  file_count: number;
  warning_count: number;
  span_count: number;
  unscanned: boolean;
};

/** SkillList is every installed skill, by name. */
export type SkillList = {
  skills: SkillItem[];
};

/** Skill is one skill in full. */
export type Skill = {
  name: string;
  description: string;
  license: string;
  compatibility: string;
  metadata: Record<string, string>;
  allowed_tools: string;
  /** body is the stored SKILL.md text after the frontmatter; span offsets are bytes into it. */
  body: string;
  source: string;
  enabled: boolean;
  broken: string;
  sha: string;
  installed_at: string;
  files: SkillFile[];
  warnings: string[];
  screen: SkillScreen | null;
};

/** SkillInstall answers an upload: the skill, disabled, and whether it was already installed unchanged. */
export type SkillInstall = {
  skill: Skill;
  unchanged: boolean;
};
