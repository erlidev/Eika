import { describe, expect, it } from "vitest";

import type { Model, ModelKind } from "@/api/types";
import { chatModels, modelsOfKind } from "@/features/providers/kind";

function model(name: string, kind: ModelKind): Model {
  return {
    id: name,
    provider_id: "p",
    kind,
    name,
    model: name,
    context_window: 8192,
    effective_window: 8192,
    max_output: 512,
    reasoning_efforts: [],
    thinking_switch: "reasoning_effort",
    preserve_thinking: false,
    image_input: false,
    created_at: "",
    updated_at: "",
  };
}

describe("modelsOfKind", () => {
  const all = [model("chat-a", "chat"), model("judge", "decision"), model("chat-b", "chat")];

  it("offers a decision model to no conversation", () => {
    expect(chatModels(all).map((m) => m.name)).toEqual(["chat-a", "chat-b"]);
  });

  it("offers the decision class only decision models", () => {
    expect(modelsOfKind(all, "decision").map((m) => m.name)).toEqual(["judge"]);
  });

  it("reads models that have not loaded as none", () => {
    expect(chatModels(undefined)).toEqual([]);
  });
});
