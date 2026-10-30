/**
 * The panel registry. The right pane is a tab strip built from this array, so
 * a phase that adds a panel writes one component and one entry here and
 * touches nothing else. The walkthrough is in docs/EXTENDING.md.
 */

import { Gauge, Play, Wrench } from "lucide-react";
import type { LucideIcon } from "lucide-react";

import { ContextPanel } from "@/features/context";
import { RunPanel, ToolsPanel } from "@/features/session";

/** PanelContext is what the workbench knows when it decides which tabs to show. */
export type PanelContext = {
  /** sessionId is the open session, empty when none is. */
  sessionId: string;
  /**
   * chat is true once the open session is known to be a chat, which has no
   * workspace. It is false while the session loads, so no tab flashes up
   * for a session that has not arrived yet.
   */
  chat: boolean;
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

const runPanel: Panel = {
  id: "run",
  title: "Run",
  icon: Play,
  available: (context) => context.sessionId !== "",
  Component: ({ sessionId }) => <RunPanel sessionId={sessionId} />,
};

const contextPanel: Panel = {
  id: "context",
  title: "Context",
  icon: Gauge,
  available: (context) => context.sessionId !== "",
  Component: ({ sessionId, chat }) => <ContextPanel sessionId={sessionId} chat={chat} />,
};

const toolsPanel: Panel = {
  id: "tools",
  title: "Tools",
  icon: Wrench,
  available: (context) => context.chat,
  Component: ({ sessionId }) => <ToolsPanel sessionId={sessionId} />,
};

/** panels is every tab the context pane can show, in tab-strip order. */
export const panels: readonly Panel[] = [runPanel, contextPanel, toolsPanel];

/** availablePanels is the tabs that apply to what is open. */
export function availablePanels(context: PanelContext): Panel[] {
  return panels.filter((panel) => panel.available(context));
}
