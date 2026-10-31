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
  ProbeResponse,
  Provider,
  Providers,
  TestModel,
  TestModelResult,
  UpdateModel,
  UpdateProvider,
} from "@/api/types";

/**
 * The models an endpoint serves are cached apart from the provider list, so
 * that recording a probe, which refreshes the list, does not ask again.
 */
const providerModelsPrefix = ["provider-models"] as const;
const providerModelsKey = (providerId: string) => [...providerModelsPrefix, providerId] as const;

/**
 * refresh invalidates what a provider or model change makes stale: the two
 * lists, the system counts, and the settings, which name the default model.
 */
async function refresh(client: QueryClient): Promise<void> {
  await Promise.all([
    client.invalidateQueries({ queryKey: queryKeys.providers() }),
    client.invalidateQueries({ queryKey: providerModelsPrefix }),
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
    onSuccess: (provider, { input }) => {
      // A new endpoint or key may be a different server: probe it again. The
      // harness records the findings, or why there are none, and the list
      // refreshes when it answers.
      if (input.base_url !== undefined || input.api_key !== undefined) {
        void probeProvider({ provider_id: provider.id })
          .catch(() => undefined)
          .then(() => client.invalidateQueries({ queryKey: queryKeys.providers() }));
      }
      return refresh(client);
    },
  });
}

/** useDeleteProvider removes a provider with its models. */
export function useDeleteProvider(): UseMutationResult<void, Error, string> {
  const client = useQueryClient();
  return useMutation({ mutationFn: deleteProvider, onSuccess: () => refresh(client) });
}

/**
 * useProbeProvider asks an endpoint which models it serves and what kind of
 * server it is. It is a mutation rather than a query because it runs when the
 * user asks, with what the form holds at that moment. When the probe was of a
 * stored provider's own endpoint the findings are recorded on it, so the list
 * is refreshed.
 */
export function useProbeProvider(): UseMutationResult<ProbeResponse, Error, ProbeProvider> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: probeProvider,
    onSuccess: (response) =>
      response.provider ? client.invalidateQueries({ queryKey: queryKeys.providers() }) : undefined,
    // A failed probe of a stored provider is recorded on it as well.
    onError: () => client.invalidateQueries({ queryKey: queryKeys.providers() }),
  });
}

/**
 * useDiscoveredModels lists the models a stored provider's endpoint serves.
 * Asking probes the endpoint, which records what kind of server it is on the
 * provider, so the provider list is refreshed with it.
 */
export function useDiscoveredModels(providerId: string): UseQueryResult<ModelInfo[]> {
  const client = useQueryClient();
  return useQuery({
    queryKey: providerModelsKey(providerId),
    queryFn: async () => {
      try {
        return (await probeProvider({ provider_id: providerId })).models;
      } finally {
        void client.invalidateQueries({ queryKey: queryKeys.providers() });
      }
    },
    staleTime: 60_000,
  });
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
