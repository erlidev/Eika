/** The configured models as the items of a select, grouped by provider. */

import type { Model, Provider } from "@/api/types";
import { SelectGroup, SelectItem, SelectLabel } from "@/components/ui/select";

type ModelSelectItemsProps = {
  models: readonly Model[];
  providers: readonly Provider[];
};

/**
 * ModelSelectItems offers every model by name under its provider. A model
 * whose provider is missing from the list still gets offered, ungrouped.
 */
export function ModelSelectItems({ models, providers }: ModelSelectItemsProps) {
  const grouped = new Set(providers.map((p) => p.id));
  const ungrouped = models.filter((m) => !grouped.has(m.provider_id));
  return (
    <>
      {providers.map((provider) => {
        const own = models.filter((m) => m.provider_id === provider.id);
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
    </>
  );
}
