/**
 * The three-pane shell: the project tree on the left, the session in the
 * middle, and the registered panels on the right. Below 1024px the side panes
 * collapse into drawers so the session still has a usable width.
 */

import { MessageCircle, PanelLeft, PanelRight, Settings, Terminal } from "lucide-react";
import { useState } from "react";
import { useNavigate, useParams } from "react-router";

import { CommandPalette } from "@/app/CommandPalette";
import { availablePanels } from "@/app/panels";
import { Sidebar } from "@/app/Sidebar";
import { ThemeToggle } from "@/app/ThemeToggle";
import { ActionError } from "@/components/Notice";
import { ResizableSplit } from "@/components/ResizableSplit";
import { Button } from "@/components/ui/button";
import { SessionView, useSession } from "@/features/session";
import { useCreateSession } from "@/features/sessions";
import { SettingsDialog, useSettingsDialog } from "@/features/settings";
import { usePersistedNumber, usePersistedString } from "@/lib/persisted";
import { nextTabIndex } from "@/lib/tablist";
import { useNarrow } from "@/lib/useNarrow";
import { cn } from "@/lib/utils";

/** narrowWidth is where the three panes stop fitting side by side. */
const narrowWidth = 1024;

/** panelMax is how wide the context pane may grow; a `fill` panel may take more. */
const panelMax = 640;
const fillPanelMax = 960;

export function Workbench() {
  const params = useParams();
  const sessionId = params.sessionId ?? "";
  const session = useSession(sessionId === "" ? undefined : sessionId);
  const workspaceId = session.data?.session.workspace_id ?? "";
  const chat = session.data !== undefined && session.data.session.workspace_id === undefined;

  const [sidebarWidth, setSidebarWidth] = usePersistedNumber("eika.pane.sidebar", 260);
  const [panelWidth, setPanelWidth] = usePersistedNumber("eika.pane.panel", 340);
  const [activePanel, setActivePanel] = usePersistedString("eika.panel.active", "tree");
  const showSettings = useSettingsDialog((s) => s.show);
  const [drawer, setDrawer] = useState<"sidebar" | "panel" | null>(null);
  const narrow = useNarrow(narrowWidth);

  const tabs = availablePanels({ sessionId, workspaceId, chat });
  const panel = tabs.find((tab) => tab.id === activePanel) ?? tabs[0];
  // A wide editor or terminal is the point of dragging the pane out; the
  // width is remembered, and a scrolling panel shows it capped again.
  const maxPanelWidth = panel?.fill === true ? fillPanelMax : panelMax;

  // A tablist is one tab stop: Tab reaches the selected tab and the arrows
  // move between them, which is what the WAI-ARIA tabs pattern asks for.
  // Selection follows the focus, so the panel below changes with it.
  const onTabKeyDown = (e: React.KeyboardEvent, from: number) => {
    const to = nextTabIndex(from, tabs.length, e.key);
    const next = to === null ? undefined : tabs[to];
    if (!next) return;
    e.preventDefault();
    setActivePanel(next.id);
    document.getElementById(`panel-tab-${next.id}`)?.focus();
  };

  const sidebar = (
    <Sidebar
      sessionId={sessionId === "" ? undefined : sessionId}
      onNavigate={() => {
        setDrawer(null);
      }}
    />
  );
  const contextPane = (
    <aside aria-label="Context panels" className="flex h-full min-h-0 flex-col border-l">
      <div
        role="tablist"
        aria-label="Context panels"
        className="flex items-center gap-0.5 border-b px-1 py-1"
      >
        {tabs.map((tab, index) => (
          <button
            key={tab.id}
            type="button"
            role="tab"
            id={`panel-tab-${tab.id}`}
            aria-selected={panel?.id === tab.id}
            aria-controls={`panel-${tab.id}`}
            tabIndex={panel?.id === tab.id ? 0 : -1}
            className={cn(
              "hover:bg-accent focus-visible:ring-ring flex items-center gap-1.5 rounded-md px-2 py-1 text-xs transition-colors focus-visible:ring-1 focus-visible:outline-none",
              panel?.id === tab.id && "bg-accent font-medium",
            )}
            onClick={() => {
              setActivePanel(tab.id);
            }}
            onKeyDown={(e) => {
              onTabKeyDown(e, index);
            }}
          >
            <tab.icon aria-hidden className="size-3.5" />
            {tab.title}
          </button>
        ))}
        {tabs.length === 0 && (
          <span className="text-muted-foreground px-2 py-1 text-xs">No panels yet.</span>
        )}
      </div>
      <div
        role="tabpanel"
        id={panel ? `panel-${panel.id}` : undefined}
        aria-labelledby={panel ? `panel-tab-${panel.id}` : undefined}
        className={cn(
          "min-h-0 flex-1",
          panel?.fill === true ? "overflow-hidden" : "overflow-y-auto",
        )}
      >
        {panel && <panel.Component sessionId={sessionId} workspaceId={workspaceId} chat={chat} />}
      </div>
    </aside>
  );

  const centre =
    sessionId === "" ? (
      <NothingOpen
        onOpened={() => {
          setDrawer(null);
        }}
      />
    ) : (
      <SessionView sessionId={sessionId} />
    );

  return (
    // The workbench is the whole window and every pane scrolls inside itself,
    // so the shell clips: nothing may scroll the page out from under the UI.
    <div className="bg-background text-foreground flex h-screen flex-col overflow-hidden">
      <header className="flex shrink-0 items-center gap-2 border-b px-2 py-1.5">
        <Terminal aria-hidden className="size-4" />
        <span className="text-sm font-semibold">Eika</span>
        {narrow && (
          <>
            <Button
              size="icon"
              variant="ghost"
              className="size-7"
              aria-label="Show projects and chats"
              aria-expanded={drawer === "sidebar"}
              onClick={() => {
                setDrawer(drawer === "sidebar" ? null : "sidebar");
              }}
            >
              <PanelLeft aria-hidden className="size-4" />
            </Button>
            <Button
              size="icon"
              variant="ghost"
              className="size-7"
              aria-label="Show panels"
              aria-expanded={drawer === "panel"}
              onClick={() => {
                setDrawer(drawer === "panel" ? null : "panel");
              }}
            >
              <PanelRight aria-hidden className="size-4" />
            </Button>
          </>
        )}
        <span className="ml-auto flex items-center gap-1">
          <CommandPalette
            onOpenSettings={() => {
              showSettings();
            }}
          />
          <ThemeToggle />
          <Button
            size="icon"
            variant="ghost"
            className="size-7"
            aria-label="Settings"
            onClick={() => {
              showSettings();
            }}
          >
            <Settings aria-hidden className="size-4" />
          </Button>
        </span>
      </header>

      {narrow ? (
        // One pane at a time: a drawer replaces the session rather than
        // sharing the width with it, which leaves neither usable.
        <main className="flex min-h-0 flex-1">
          {drawer === "sidebar" && <div className="min-w-0 flex-1">{sidebar}</div>}
          {drawer === "panel" && <div className="min-w-0 flex-1">{contextPane}</div>}
          {drawer === null && <div className="flex min-w-0 flex-1 flex-col">{centre}</div>}
        </main>
      ) : (
        <main className="flex min-h-0 flex-1">
          <ResizableSplit
            side="start"
            width={sidebarWidth}
            onWidthChange={setSidebarWidth}
            min={180}
            max={480}
            label="Resize the project tree"
            panel={<div className="h-full border-r">{sidebar}</div>}
          >
            <ResizableSplit
              side="end"
              width={Math.min(panelWidth, maxPanelWidth)}
              onWidthChange={setPanelWidth}
              min={240}
              max={maxPanelWidth}
              label="Resize the context panels"
              panel={contextPane}
            >
              {centre}
            </ResizableSplit>
          </ResizableSplit>
        </main>
      )}

      <SettingsDialog />
    </div>
  );
}

/**
 * NothingOpen is the centre pane before a session is picked: where to find
 * one, and the one action that needs no project, starting a chat.
 */
function NothingOpen({ onOpened }: { onOpened: () => void }) {
  const create = useCreateSession();
  const navigate = useNavigate();
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center">
      <p className="text-muted-foreground text-sm">
        Pick a session or a chat on the left, or create a session in a workspace.
      </p>
      <Button
        size="sm"
        variant="outline"
        disabled={create.isPending}
        onClick={() => {
          create.mutate(
            { chat: true, title: "New chat" },
            {
              onSuccess: (created) => {
                onOpened();
                void navigate(`/sessions/${created.id}`);
              },
            },
          );
        }}
      >
        <MessageCircle aria-hidden className="size-3.5" />
        New chat
      </Button>
      {create.isError && <ActionError action="start a chat" error={create.error} />}
    </div>
  );
}
