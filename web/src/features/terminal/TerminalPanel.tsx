/**
 * The Terminal panel: a shell inside the workspace's sandbox, drawn by xterm.
 * The shell itself lives in `session.ts`, so leaving the tab and coming back
 * finds it where it was.
 */

import { lazy, Suspense } from "react";

import { Notice } from "@/components/Notice";
import { RunningWorkspace } from "@/features/workspaces";

const ShellView = lazy(() =>
  import("@/features/terminal/ShellView").then((module) => ({ default: module.ShellView })),
);

export type TerminalPanelProps = {
  workspaceId: string;
};

/**
 * TerminalPanel shows the workspace's shell. Opening it for another workspace
 * ends the previous workspace's shell, so one runs at a time.
 */
export function TerminalPanel({ workspaceId }: TerminalPanelProps) {
  return (
    <RunningWorkspace workspaceId={workspaceId} purpose="open a shell">
      <Suspense
        fallback={
          <div className="p-3">
            <Notice tone="pending">Loading the terminal…</Notice>
          </div>
        }
      >
        <ShellView workspaceId={workspaceId} />
      </Suspense>
    </RunningWorkspace>
  );
}
