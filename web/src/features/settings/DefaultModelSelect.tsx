/** The choice of the model a run uses when a message names none. */

import { LoadError } from "@/components/Notice";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useModels, useProviders } from "@/features/providers";
import { settingKeys, useSaveSettings } from "@/features/settings/queries";

export function DefaultModelSelect() {
  const models = useModels();
  const providers = useProviders();
  const save = useSaveSettings();
  const all = models.data?.models ?? [];
  const loadFailed = models.error ?? providers.error;
  // A model whose provider is missing from the list still gets offered.
  const grouped = new Set((providers.data?.providers ?? []).map((p) => p.id));
  const ungrouped = all.filter((m) => !grouped.has(m.provider_id));

  return (
    <div className="space-y-1.5">
      <Label htmlFor="default-model">Default model</Label>
      <Select
        value={models.data?.default ?? ""}
        disabled={all.length === 0}
        aria-describedby="default-model-hint"
        onValueChange={(value) => {
          save.mutate({ [settingKeys.defaultModel]: value });
        }}
      >
        <SelectTrigger id="default-model" className="w-full">
          <SelectValue placeholder={models.isPending ? "Loading the models…" : "No models yet"} />
        </SelectTrigger>
        <SelectContent>
          {(providers.data?.providers ?? []).map((provider) => {
            const own = all.filter((m) => m.provider_id === provider.id);
            if (own.length === 0) return null;
            return (
              <SelectGroup key={provider.id}>
                <SelectLabel>{provider.name}</SelectLabel>
                {own.map((model) => (
                  <SelectItem key={model.id} value={model.name}>
                    {model.name}
                  </SelectItem>
                ))}
              </SelectGroup>
            );
          })}
          {ungrouped.map((model) => (
            <SelectItem key={model.id} value={model.name}>
              {model.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <p id="default-model-hint" className="text-muted-foreground text-xs">
        {models.isSuccess && all.length === 0
          ? "Add a model first, under Settings, Models; the choice opens once there is one."
          : "Used when a message names no model. A session can pick another above its composer."}
      </p>
      {loadFailed && (
        <LoadError
          what={models.isError ? "the models" : "the providers"}
          error={loadFailed}
          retrying={models.isFetching || providers.isFetching}
          retry={() => {
            if (models.isError) void models.refetch();
            if (providers.isError) void providers.refetch();
          }}
        />
      )}
      {save.isError && (
        <p role="alert" className="text-destructive text-xs">
          {save.error.message}
        </p>
      )}
    </div>
  );
}
