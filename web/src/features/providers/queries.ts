/** Server state for model providers and models. Every such route is reached through here. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { QueryClient, UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import {
  createModel,
  createProvider,
  deleteModel,
  deleteProvider,
  getModels,
  listProviders,
  probeProvider,
  testModel,
  updateModel,
  updateProvider,
} from "@/api/routes";
import type {
  CreateModel,
  CreateProvider,
  Model,
  ModelInfo,
  Models,
  ProbeProvider,
  Provider,
  Providers,
  TestModel,
  TestModelResult,
  UpdateModel,
  UpdateProvider,
} from "@/api/types";

/**
 * refresh invalidates what a provider or model change makes stale: the two
 * lists, the system counts, and the settings, which name the default model.
 */
async function refresh(client: QueryClient): Promise<void> {
  await Promise.all([
    client.invalidateQueries({ queryKey: queryKeys.providers() }),
    client.invalidateQueries({ queryKey: queryKeys.models() }),
    client.invalidateQueries({ queryKey: queryKeys.system() }),
    client.invalidateQueries({ queryKey: queryKeys.settings() }),
  ]);
}

/** useProviders lists the providers and the kinds a new one may have. */
export function useProviders(): UseQueryResult<Providers> {
  return useQuery({
    queryKey: queryKeys.providers(),
    queryFn: ({ signal }) => listProviders(signal),
  });
}

/** useModels lists the models and the one a run uses by default. */
export function useModels(): UseQueryResult<Models> {
  return useQuery({
    queryKey: queryKeys.models(),
    queryFn: ({ signal }) => getModels(signal),
  });
}

/** useCreateProvider records a provider. */
export function useCreateProvider(): UseMutationResult<Provider, Error, CreateProvider> {
  const client = useQueryClient();
  return useMutation({ mutationFn: createProvider, onSuccess: () => refresh(client) });
}

/** useUpdateProvider changes a provider's name, endpoint, or key. */
export function useUpdateProvider(): UseMutationResult<
  Provider,
  Error,
  { id: string; input: UpdateProvider }
> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }) => updateProvider(id, input),
    onSuccess: () => refresh(client),
  });
}

/** useDeleteProvider removes a provider with its models. */
export function useDeleteProvider(): UseMutationResult<void, Error, string> {
  const client = useQueryClient();
  return useMutation({ mutationFn: deleteProvider, onSuccess: () => refresh(client) });
}

/**
 * useProbeProvider asks an endpoint which models it serves. It is a mutation
 * rather than a query because it runs when the user asks, with what the form
 * holds at that moment.
 */
export function useProbeProvider(): UseMutationResult<ModelInfo[], Error, ProbeProvider> {
  return useMutation({ mutationFn: probeProvider });
}

/** useCreateModel adds a model to a provider. */
export function useCreateModel(): UseMutationResult<Model, Error, CreateModel> {
  const client = useQueryClient();
  return useMutation({ mutationFn: createModel, onSuccess: () => refresh(client) });
}

/**
 * useModel is the model a name selects, or the default model when the name is
 * empty, which is what an unset session-level choice means.
 */
export function useModel(name: string): Model | undefined {
  const models = useModels();
  const wanted = name === "" ? (models.data?.default ?? "") : name;
  return (models.data?.models ?? []).find((m) => m.name === wanted);
}

/** useUpdateModel changes a model. */
export function useUpdateModel(): UseMutationResult<
  Model,
  Error,
  { id: string; input: UpdateModel }
> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }) => updateModel(id, input),
    onSuccess: () => refresh(client),
  });
}

/** useDeleteModel removes a model. */
export function useDeleteModel(): UseMutationResult<void, Error, string> {
  const client = useQueryClient();
  return useMutation({ mutationFn: deleteModel, onSuccess: () => refresh(client) });
}

/** useTestModel sends one small request to a model. */
export function useTestModel(): UseMutationResult<TestModelResult, Error, TestModel> {
  return useMutation({ mutationFn: testModel });
}
