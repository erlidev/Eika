import { describe, expect, it } from "vitest";

import { composerModel, imageAdvice } from "@/features/session/composerModel";
import type { ComposerModelInput } from "@/features/session/composerModel";

const base: ComposerModelInput = {
  sessionKnown: true,
  configurationSettled: true,
  modelsKnown: true,
  agent: false,
  chosen: "",
  resolved: "gpt-5",
  defaultModel: "gpt-5-mini",
  models: [
    { name: "gpt-5", image_input: false },
    { name: "gpt-5-mini", image_input: false },
    { name: "vision", image_input: true },
  ],
};

describe("composerModel", () => {
  it("is not ready until the session, its configuration and the models are known", () => {
    expect(composerModel({ ...base, sessionKnown: false }).ready).toBe(false);
    expect(composerModel({ ...base, configurationSettled: false }).ready).toBe(false);
    expect(composerModel({ ...base, modelsKnown: false }).ready).toBe(false);
    expect(composerModel(base).ready).toBe(true);
  });

  it("never names a model for an agent session, whatever was chosen", () => {
    const home = composerModel({ ...base, agent: true, chosen: "gpt-5-mini", resolved: "vision" });
    expect(home.runModel).toBe("");
    expect(home.label).toBe("vision");
    // The images follow the model its class resolves to, not the default.
    expect(home.imageInput).toBe(true);
  });

  it("does not fall back to the default model for an agent whose model is not known", () => {
    const home = composerModel({ ...base, agent: true, resolved: undefined });
    expect(home.label).toBe("");
    expect(home.imageInput).toBe(false);
  });

  it("blocks images until everything is known", () => {
    const loading = composerModel({ ...base, resolved: "vision", configurationSettled: false });
    expect(loading.imageInput).toBe(false);
    expect(composerModel({ ...base, resolved: "vision" }).imageInput).toBe(true);
  });

  it("sends the chosen model of an ordinary session, else what it resolves to", () => {
    expect(composerModel({ ...base, chosen: "vision" }).runModel).toBe("vision");
    expect(composerModel(base).runModel).toBe("gpt-5");
    expect(composerModel({ ...base, resolved: undefined }).runModel).toBe("gpt-5-mini");
  });
});

describe("imageAdvice", () => {
  it("does not tell an agent session's user to choose a model", () => {
    expect(imageAdvice(false)).toContain("Choose a model");
    expect(imageAdvice(true)).not.toContain("Choose a model");
    expect(imageAdvice(true)).toContain("class");
  });
});
