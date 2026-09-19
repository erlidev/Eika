/** The choice of the model a run uses when a message names none. */

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

  return (
    <div className="space-y-1.5">
      <Label htmlFor="default-model">Default model</Label>
      <Select
        value={models.data?.default ?? ""}
        disabled={all.length === 0}
        onValueChange={(value) => {
          save.mutate({ [settingKeys.defaultModel]: value });
        }}
      >
        <SelectTrigger id="default-model" className="w-full">
          <SelectValue placeholder="No models yet" />
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
        </SelectContent>
      </Select>
      <p className="text-muted-foreground text-xs">
        Used when a message names no model. A session can pick another above its composer.
      </p>
      {save.isError && (
        <p role="alert" className="text-destructive text-xs">
          {save.error.message}
        </p>
      )}
    </div>
  );
}
