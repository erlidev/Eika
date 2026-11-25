import { describe, expect, it } from "vitest";

import { composerModel } from "@/features/session/composerModel";
import type { ComposerModelInput } from "@/features/session/composerModel";

const base: ComposerModelInput = {
  sessionKnown: true,
  predictionSettled: true,
  modelsKnown: true,
  agent: false,
  chosen: "",
  resolved: "gpt-5",
  models: [
    { name: "gpt-5", image_input: false },
    { name: "gpt-5-mini", image_input: false },
    { name: "vision", image_input: true },
  ],
};

describe("composerModel", () => {
  it("is not ready until the session, its prediction and the models are known", () => {
    expect(composerModel({ ...base, sessionKnown: false }).ready).toBe(false);
    expect(composerModel({ ...base, predictionSettled: false }).ready).toBe(false);
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

  it("waits for an agent model before allowing a message", () => {
    const home = composerModel({ ...base, agent: true, resolved: undefined });
    expect(home.ready).toBe(false);
    expect(home.label).toBe("");
    expect(home.imageInput).toBe(false);
  });

  it("blocks images until everything is known", () => {
    const loading = composerModel({ ...base, resolved: "vision", predictionSettled: false });
    expect(loading.imageInput).toBe(false);
    expect(composerModel({ ...base, resolved: "vision" }).imageInput).toBe(true);
  });

  it("sends the chosen model of an ordinary session, else what it resolves to", () => {
    expect(composerModel({ ...base, chosen: "vision" }).runModel).toBe("vision");
    expect(composerModel(base).runModel).toBe("gpt-5");
    expect(composerModel({ ...base, resolved: undefined }).runModel).toBe("");
  });
});
