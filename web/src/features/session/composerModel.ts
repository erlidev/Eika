/**
 * Which model the composer sends with a message, which it names, and whether
 * it takes images. Pure, so the cases that went wrong while queries were still
 * loading are tested without a browser.
 */

/** ComposerModelInput is what the session view knows when it decides. */
export type ComposerModelInput = {
  /** sessionKnown is true once the session itself has loaded: until then it may be an agent's. */
  sessionKnown: boolean;
  /** configurationSettled is true once the session's configuration has loaded or failed. */
  configurationSettled: boolean;
  /** modelsKnown is true once the model list has loaded. */
  modelsKnown: boolean;
  /** agent says the session runs an agent, whose class picks the model. */
  agent: boolean;
  /** chosen is the model picked in this browser's status bar; empty when none was. */
  chosen: string;
  /** resolved is the model the session's configuration resolves to, once known. */
  resolved: string | undefined;
  /** defaultModel is the deployment's default chat model. */
  defaultModel: string;
  /** models are the chat models, with whether each takes images. */
  models: readonly { name: string; image_input: boolean }[];
};

/** ComposerModel is the composer's answer. */
export type ComposerModel = {
  /** ready is false until the session is known, so no message goes out with a guessed model. */
  ready: boolean;
  /** runModel is what a request names: empty for an agent, which the harness refuses a model for. */
  runModel: string;
  /** label is the model the page names. */
  label: string;
  /** imageInput is whether that model accepts images; false until it is known. */
  imageInput: boolean;
};

/** composerModel decides the model of the next message. */
export function composerModel(input: ComposerModelInput): ComposerModel {
  const ready = input.sessionKnown && input.configurationSettled && input.modelsKnown;
  // An agent's model is its class's, which only the configuration knows: the
  // browser's choice and the default are not it.
  const label = input.agent
    ? (input.resolved ?? "")
    : input.chosen !== ""
      ? input.chosen
      : (input.resolved ?? input.defaultModel);
  const imageInput = ready && (input.models.find((m) => m.name === label)?.image_input ?? false);
  return { ready, runModel: input.agent ? "" : label, label, imageInput };
}

/**
 * imageAdvice is what to do about a model that takes no images. On an agent
 * session the user cannot pick a model: its class does, under Settings.
 */
export function imageAdvice(agent: boolean): string {
  return agent
    ? "Turn on Image input for that model under Settings, Models, or give its class a model that accepts images under Settings, General."
    : "Choose a model that does, or turn on Image input for it under Settings, Models.";
}
