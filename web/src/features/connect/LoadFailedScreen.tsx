/**
 * What a signed-in browser sees when its first load of the settings fails
 * for a reason other than a refused token: the harness is down, starting, or
 * broken. The browser is still signed in, so a password form would only
 * mislead; the screen says what failed and offers Retry.
 */

import { CircleAlert, RotateCw } from "lucide-react";

import { Screen, ScreenHeader, ScreenMark } from "@/components/Screen";
import { Button } from "@/components/ui/button";
import { failureText } from "@/lib/failure";

export type LoadFailedScreenProps = {
  /** what names what failed to load, such as "the settings". */
  what: string;
  error: Error;
  retry: () => void;
  retrying?: boolean;
};

export function LoadFailedScreen({ what, error, retry, retrying = false }: LoadFailedScreenProps) {
  return (
    <Screen role="alert" className="space-y-4">
      <ScreenHeader
        mark={<ScreenMark icon={CircleAlert} tone="error" />}
        title="Eika could not load"
      >
        <p className="text-foreground">{failureText(`load ${what}`, error)}</p>
        <p className="mt-2 text-xs">
          This browser is still signed in; nothing needs to be entered again.
        </p>
      </ScreenHeader>
      <Button type="button" className="w-full" disabled={retrying} onClick={retry}>
        <RotateCw aria-hidden className={retrying ? "animate-spin" : ""} />
        {retrying ? "Retrying…" : "Retry"}
      </Button>
    </Screen>
  );
}
