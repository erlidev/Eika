/** The choice of the model a run uses when a message names none. */

import { LoadError } from "@/components/Notice";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useModels, useProviders } from "@/features/providers";
import { ModelSelectItems } from "@/features/settings/ModelSelectItems";
import { settingKeys, useSaveSettings } from "@/features/settings/queries";
import { failureText } from "@/lib/failure";

export function DefaultModelSelect() {
  const models = useModels();
  const providers = useProviders();
  const save = useSaveSettings();
  const all = models.data?.models ?? [];
  const loadFailed = models.error ?? providers.error;

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
          <SelectValue
            placeholder={
              models.isError
                ? "The models did not load; see below"
                : models.isPending
                  ? "Loading the models…"
                  : "No models yet"
            }
          />
        </SelectTrigger>
        <SelectContent>
          <ModelSelectItems models={all} providers={providers.data?.providers ?? []} />
        </SelectContent>
      </Select>
      <p id="default-model-hint" className="text-muted-foreground text-xs">
        {models.isSuccess && all.length === 0
          ? "Add a model first, under Settings, Models; the choice opens once there is one."
          : "Used when a message names no model. A session can pick another in its status bar."}
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
          {failureText("change the default model", save.error)}
        </p>
      )}
    </div>
  );
}
