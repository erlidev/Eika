/**
 * The steps of the guided setup and where it starts. Pure, so the order is
 * tested apart from the screens that draw it.
 */

/** SetupStep names one step of the guided setup. */
export type SetupStep = "account" | "provider" | "models" | "system" | "project";

/** SetupStepInfo is what the stepper and the step's heading say. */
export type SetupStepInfo = {
  id: SetupStep;
  /** label is the stepper's short name. */
  label: string;
  title: string;
  description: string;
};

/** setupSteps are the steps in the order the wizard walks them. */
export const setupSteps: readonly SetupStepInfo[] = [
  {
    id: "account",
    label: "Password",
    title: "Welcome to Eika",
    description:
      "Choose the password this harness signs in with. Eika is single-user: whoever knows the password can run agents on this machine.",
  },
  {
    id: "provider",
    label: "Provider",
    title: "Connect a model provider",
    description:
      "Agents need a model to think with. Pick where it comes from: any OpenAI-compatible endpoint works, hosted or on this machine.",
  },
  {
    id: "models",
    label: "Models",
    title: "Choose models",
    description:
      "Tick the models agents may use. You can add more, and more providers, in Settings whenever you like.",
  },
  {
    id: "system",
    label: "Sandbox",
    title: "Check the sandbox",
    description:
      "Agents work only inside Docker containers, never on this machine directly. Eika checks that it can start them.",
  },
  {
    id: "project",
    label: "Project",
    title: "Add your first project",
    description:
      "A project is a git repository agents work on: a directory on this machine, or a remote such as GitHub.",
  },
];

/** SetupState is what decides where the setup starts. */
export type SetupState = {
  /** passwordSet is false until a sign-in password exists. */
  passwordSet: boolean;
  providers: number;
  models: number;
};

/**
 * firstStep is where the setup starts: the first thing still missing. Coming
 * back to a setup left halfway resumes it rather than starting over.
 */
export function firstStep(state: SetupState): SetupStep {
  if (!state.passwordSet) return "account";
  if (state.providers === 0) return "provider";
  if (state.models === 0) return "models";
  return "system";
}

/** stepInfo returns a step's description. */
export function stepInfo(step: SetupStep): SetupStepInfo {
  const found = setupSteps.find((s) => s.id === step);
  if (!found) throw new Error(`setup step ${step} is not defined`);
  return found;
}

/** stepIndex is a step's zero-based position. */
export function stepIndex(step: SetupStep): number {
  return setupSteps.findIndex((s) => s.id === step);
}

/** nextStep returns the step after `step`, or null after the last. */
export function nextStep(step: SetupStep): SetupStep | null {
  return setupSteps[stepIndex(step) + 1]?.id ?? null;
}
