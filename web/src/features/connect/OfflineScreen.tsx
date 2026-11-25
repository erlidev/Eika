/**
 * What a signed-out browser sees when Eika cannot be asked whether it is set
 * up: the network is down, or Eika is stopped or starting. Signing in is not
 * possible yet, so a password form would only mislead. "Use another address"
 * is the way to the sign-in screen's harness URL, for a browser pointed at the
 * wrong place.
 */

import { WifiOff } from "lucide-react";
import { useState } from "react";

import { Screen, ScreenHeader, ScreenMark } from "@/components/Screen";
import { Button } from "@/components/ui/button";
import { SignInScreen } from "@/features/connect/SignInScreen";

export type OfflineScreenProps = {
  error: Error;
  retry: () => void;
  retrying?: boolean;
};

export function OfflineScreen({ error, retry, retrying = false }: OfflineScreenProps) {
  const [another, setAnother] = useState(false);
  if (another)
    return <SignInScreen unreachable={error.message} retry={retry} retrying={retrying} />;
  return (
    <Screen role="alert" className="space-y-4">
      <ScreenHeader mark={<ScreenMark icon={WifiOff} tone="error" />} title="Eika is offline">
        <p className="text-foreground">
          {navigator.onLine
            ? "Eika did not answer. It may be starting or stopped."
            : "This device has no network connection."}
        </p>
      </ScreenHeader>
      <Button type="button" className="w-full" disabled={retrying} onClick={retry}>
        {retrying ? "Retrying…" : "Retry"}
      </Button>
      <Button
        type="button"
        variant="link"
        className="w-full"
        onClick={() => {
          setAnother(true);
        }}
      >
        Use another address
      </Button>
    </Screen>
  );
}
