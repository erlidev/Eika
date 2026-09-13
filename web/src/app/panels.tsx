/**
 * The panel registry. The right pane is a tab strip built from this array, so
 * a phase that adds a panel writes one component and one entry here and
 * touches nothing else. The walkthrough is in docs/EXTENDING.md.
 */

import { ListTree, Play } from "lucide-react";
import type { LucideIcon } from "lucide-react";

import { RunPanel, SessionTreePanel } from "@/features/session";

/** PanelContext is what the workbench knows when it decides which tabs to show. */
export type PanelContext = {
  /** sessionId is the open session, empty when none is. */
  sessionId: string;
  /** workspaceId is that session's workspace, empty when none is open. */
  workspaceId: string;
};

/** PanelProps is what a panel component receives. */
export type PanelProps = PanelContext;

/** Panel is one tab of the context pane. */
export type Panel = {
  /** id is the tab's stable name; it is what the layout remembers. */
  id: string;
  /** title is the tab label. */
  title: string;
  /** icon is drawn beside the label. */
  icon: LucideIcon;
  /** available hides the tab when its data cannot exist yet. */
  available: (context: PanelContext) => boolean;
  /** Component renders the tab's content. */
  Component: (props: PanelProps) => React.ReactNode;
};

const sessionTreePanel: Panel = {
  id: "tree",
  title: "Tree",
  icon: ListTree,
  available: (context) => context.sessionId !== "",
  Component: ({ sessionId }) => <SessionTreePanel sessionId={sessionId} />,
};

const runPanel: Panel = {
  id: "run",
  title: "Run",
  icon: Play,
  available: (context) => context.sessionId !== "",
  Component: ({ sessionId }) => <RunPanel sessionId={sessionId} />,
};

/**
 * panels is every tab the context pane can show, in tab-strip order. Phases 6
 * and 8 append their agents, diff, files, and terminal panels here.
 */
export const panels: readonly Panel[] = [sessionTreePanel, runPanel];

/** availablePanels is the tabs that apply to what is open. */
export function availablePanels(context: PanelContext): Panel[] {
  return panels.filter((panel) => panel.available(context));
}
