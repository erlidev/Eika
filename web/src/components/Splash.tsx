/** The whole-page placeholder shown while the app finds out where it stands. */

import { Loader2, Terminal } from "lucide-react";

import { ScreenMark } from "@/components/Screen";

export type SplashProps = {
  message?: string;
};

export function Splash({ message = "Connecting to Eika…" }: SplashProps) {
  return (
    <main className="bg-background text-foreground flex min-h-screen flex-col items-center justify-center gap-3">
      <ScreenMark icon={Terminal} />
      <p className="text-muted-foreground flex items-center gap-2 text-sm" role="status">
        <Loader2 aria-hidden className="size-3.5 animate-spin" />
        {message}
      </p>
    </main>
  );
}
