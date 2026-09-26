/**
 * The Search tab: which web providers the failover chain tries and in what
 * order, their API keys, the quotas that keep them inside a free tier, the
 * health of every backend, and a box to try a search the way an agent would.
 */

import { LoadError, Notice } from "@/components/Notice";
import { Separator } from "@/components/ui/separator";
import { searchSettingKeys, useSearchStatus } from "@/features/search";
import { useSaveSettings, useSettings } from "@/features/settings/queries";
import { SearchKeys } from "@/features/settings/SearchKeys";
import { ProviderOrder, Sources } from "@/features/settings/SearchProviders";
import { Quotas } from "@/features/settings/SearchQuotas";
import { TrySearch } from "@/features/settings/TrySearch";

export function SearchSettings() {
  const settings = useSettings();
  const status = useSearchStatus();
  // The saves live here, above the forms they serve, so that a form that
  // remounts on the value it saved still shows how the save went.
  const saveOrder = useSaveSettings();
  const saveLimits = useSaveSettings();
  if (settings.isError) {
    return (
      <LoadError
        what="the settings"
        error={settings.error}
        retrying={settings.isFetching}
        retry={() => void settings.refetch()}
      />
    );
  }
  if (status.isError) {
    return (
      <LoadError
        what="the search status"
        error={status.error}
        retrying={status.isFetching}
        retry={() => void status.refetch()}
      />
    );
  }
  if (!settings.data || !status.data) return <Notice tone="pending">Loading search…</Notice>;
  return (
    <div className="space-y-6">
      {/* Keyed by the stored value, so a form starts from what is stored now
          and a save elsewhere leaves its unsaved edits alone. */}
      <ProviderOrder
        key={`order-${JSON.stringify(settings.data.settings[searchSettingKeys.order] ?? null)}`}
        state={settings.data}
        status={status.data}
        save={saveOrder}
      />
      <Separator />
      <SearchKeys status={status.data} />
      <Separator />
      <Quotas
        key={`limits-${JSON.stringify(settings.data.settings[searchSettingKeys.limits] ?? null)}`}
        state={settings.data}
        save={saveLimits}
      />
      <Separator />
      <Sources status={status.data} />
      <Separator />
      <TrySearch />
    </div>
  );
}
