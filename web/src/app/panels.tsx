/**
 * The panel registry. The right pane is a tab strip built from this array, so
 * a phase that adds a panel writes one component and one entry here and
 * touches nothing else. The walkthrough is in docs/EXTENDING.md.
 */

import { FileCode, GitCompare, ListTree, Play, SquareTerminal, Wrench } from "lucide-react";
import type { LucideIcon } from "lucide-react";

import { ChangesPanel } from "@/features/changes";
import { FilesPanel } from "@/features/files";
import { RunPanel, SessionTreePanel, ToolsPanel } from "@/features/session";
import { TerminalPanel } from "@/features/terminal";

/** PanelContext is what the workbench knows when it decides which tabs to show. */
export type PanelContext = {
  /** sessionId is the open session, empty when none is. */
  sessionId: string;
  /** workspaceId is that session's workspace, empty when none is open. */
  workspaceId: string;
  /**
   * chat is true once the open session is known to be a chat, which has no
   * workspace. It is false while the session loads, so no tab flashes up
   * for a workspace session that has not arrived yet.
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
  /**
   * fill gives the panel the whole pane body, which does not scroll, and a
   * wider pane limit: for a panel that sizes and scrolls its own content,
   * such as a terminal or an editor.
   */
  fill?: boolean;
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

const filesPanel: Panel = {
  id: "files",
  title: "Files",
  icon: FileCode,
  available: (context) => context.workspaceId !== "",
  fill: true,
  Component: ({ workspaceId }) => <FilesPanel workspaceId={workspaceId} />,
};

const terminalPanel: Panel = {
  id: "terminal",
  title: "Terminal",
  icon: SquareTerminal,
  available: (context) => context.workspaceId !== "",
  fill: true,
  Component: ({ workspaceId }) => <TerminalPanel workspaceId={workspaceId} />,
};

const toolsPanel: Panel = {
  id: "tools",
  title: "Tools",
  icon: Wrench,
  available: (context) => context.chat,
  Component: ({ sessionId }) => <ToolsPanel sessionId={sessionId} />,
};

const changesPanel: Panel = {
  id: "changes",
  title: "Changes",
  icon: GitCompare,
  available: (context) => context.workspaceId !== "",
  Component: ({ workspaceId }) => <ChangesPanel workspaceId={workspaceId} />,
};

/**
 * panels is every tab the context pane can show, in tab-strip order. A chat
 * has no workspace, so where a workspace session shows its files, terminal,
 * and changes, a chat shows its tools.
 */
export const panels: readonly Panel[] = [
  sessionTreePanel,
  runPanel,
  toolsPanel,
  filesPanel,
  terminalPanel,
  changesPanel,
];

/** availablePanels is the tabs that apply to what is open. */
export function availablePanels(context: PanelContext): Panel[] {
  return panels.filter((panel) => panel.available(context));
}
