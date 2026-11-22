/**
 * The halt banners under the header, on every page: "Eika is halted" with its
 * reason and a Resume button, and, if pressing Halt failed, the failure with a
 * Halt button to try again, since a halt that did not happen must not pass for
 * one that did.
 */

import { OctagonX } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useHalt, useHaltNow, useResume } from "@/features/halt/queries";
import { failureText } from "@/lib/failure";
import { cn } from "@/lib/utils";

const bannerClass =
  "border-destructive/30 bg-destructive/10 text-destructive flex shrink-0 items-center gap-2 border-b px-3 py-1.5 text-xs";

export function HaltBanner() {
  const state = useHalt();
  const halt = useHaltNow();
  const resume = useResume();

  if (halt.isError && state.data?.halted !== true) {
    return (
      <div role="alert" className={bannerClass}>
        <OctagonX aria-hidden className="size-3.5 shrink-0" />
        <p className="min-w-0 flex-1 break-words">{failureText("halt Eika", halt.error)}</p>
        <Button
          type="button"
          size="xs"
          variant="outline"
          className="border-destructive/40"
          onClick={() => {
            halt.mutate();
          }}
        >
          Halt
        </Button>
      </div>
    );
  }
  if (state.data?.halted !== true) return null;
  const reason = state.data.reason ?? "";
  return (
    <div role="status" className={cn(bannerClass, "items-start")}>
      <OctagonX aria-hidden className="mt-0.5 size-3.5 shrink-0" />
      <div className="min-w-0 flex-1 break-words">
        <p className="font-medium">Eika is halted</p>
        {reason !== "" && <p className="opacity-80">{reason}</p>}
        {resume.isError && <p role="alert">{failureText("resume Eika", resume.error)}</p>}
      </div>
      <Button
        type="button"
        size="xs"
        variant="outline"
        className="border-destructive/40"
        disabled={resume.isPending}
        onClick={() => {
          resume.mutate();
        }}
      >
        Resume
      </Button>
    </div>
  );
}
