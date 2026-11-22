import { useGuard } from "@/features/settings/guard";
import { useSettings } from "@/features/settings/queries";

export function GuardBanner() {
  const guard = useGuard();
  const settings = useSettings();
  if (guard.data?.status !== "down") return null;
  const required =
    settings.data?.settings.screen_required ?? settings.data?.defaults.screen_required;
  return (
    <div
      role="status"
      className="border-destructive/30 bg-destructive/10 text-destructive shrink-0 border-b px-3 py-1.5 text-xs"
    >
      The guard is not responding.{" "}
      {required === true
        ? "Untrusted content is blocked."
        : required === false
          ? "Untrusted content may reach the model unscanned."
          : "Check Security settings for how untrusted content is handled."}
    </div>
  );
}
