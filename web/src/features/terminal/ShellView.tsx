/**
 * The shell view: the terminal attached to the panel, and the bar above it
 * that says whether the shell is connected. It is its own module so that
 * xterm loads only when the Terminal panel first opens.
 */

import { RotateCcw } from "lucide-react";
import { useEffect, useMemo, useRef, useSyncExternalStore } from "react";

import { Button } from "@/components/ui/button";
import { shellSession } from "@/features/terminal/session";
import type { ShellSession, ShellState } from "@/features/terminal/session";

export type ShellViewProps = {
  workspaceId: string;
};

export function ShellView({ workspaceId }: ShellViewProps) {
  // shellSession is idempotent for one workspace, so reading it during render is safe.
  const session = useMemo(() => shellSession(workspaceId), [workspaceId]);
  const state = useSyncExternalStore(session.subscribe, session.getState);
  const container = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const element = container.current;
    if (!element) return;
    // The terminal does not take the focus: the tab strip's arrow keys must
    // keep moving between tabs. A click puts the keyboard in the terminal.
    void session.attach(element);
    const observer = new ResizeObserver(() => {
      session.resize();
    });
    observer.observe(element);
    return () => {
      observer.disconnect();
      session.detach();
    };
  }, [session]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <ShellBar session={session} state={state} />
      <div
        ref={container}
        role="group"
        aria-label="Terminal"
        className="bg-background min-h-0 flex-1 overflow-hidden px-1.5 py-1"
      />
    </div>
  );
}

const statusText: Record<ShellState["status"], string> = {
  connecting: "Connecting…",
  open: "Connected",
  exited: "Exited",
  closed: "Disconnected",
  failed: "Could not connect to the shell. The workspace may have stopped; try again.",
};

function ShellBar({ session, state }: { session: ShellSession; state: ShellState }) {
  const ended = state.status !== "connecting" && state.status !== "open";
  return (
    <div className="text-muted-foreground flex h-8 shrink-0 items-center gap-2 border-b px-2 text-xs">
      <span role="status" className="min-w-0 truncate">
        {state.status === "exited"
          ? `Shell exited with status ${String(state.exitCode ?? 0)}`
          : statusText[state.status]}
      </span>
      {ended && (
        <Button
          type="button"
          size="xs"
          variant="outline"
          className="ml-auto"
          onClick={() => {
            session.restart();
          }}
        >
          <RotateCcw aria-hidden className="size-3" />
          {state.status === "exited" ? "Restart" : "Reconnect"}
        </Button>
      )}
    </div>
  );
}
