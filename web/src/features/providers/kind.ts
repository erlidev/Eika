/** What a model answers, and so where it may be offered. */

import type { Model, ModelKind } from "@/api/types";

/**
 * modelsOfKind keeps the models of one kind, in order. A decision model
 * answers typed questions and cannot hold a conversation, so every place that
 * offers a model for one lists only the chat models; a class that takes a
 * decision model lists only those.
 */
export function modelsOfKind(models: readonly Model[] | undefined, kind: ModelKind): Model[] {
  return (models ?? []).filter((m) => m.kind === kind);
}

/** chatModels keeps the models a conversation can run on. */
export function chatModels(models: readonly Model[] | undefined): Model[] {
  return modelsOfKind(models, "chat");
}
