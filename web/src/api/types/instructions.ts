/** GlobalInstructions contains text that only the user can write. */
export type GlobalInstructions = {
  about_me: string;
  rules: string;
};

/** ProjectInstructions is separate from agent-editable project metadata. */
export type ProjectInstructions = {
  instructions: string;
  version: number;
};
