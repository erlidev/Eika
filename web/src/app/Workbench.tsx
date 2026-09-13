/**
 * The three-pane shell: the project tree on the left, the session in the
 * middle, and the registered panels on the right. Below 1024px the side panes
 * collapse into drawers so the session still has a usable width.
 */

import { PanelLeft, PanelRight, Settings, Terminal } from "lucide-react";
import { useState } from "react";
import { useParams } from "react-router";

import { useSession } from "@/features/session";
import { CommandPalette } from "@/app/CommandPalette";
import { availablePanels } from "@/app/panels";
import { Sidebar } from "@/app/Sidebar";
import { ThemeToggle } from "@/app/ThemeToggle";
import { ResizableSplit } from "@/components/ResizableSplit";
import { Button } from "@/components/ui/button";
import { SessionView } from "@/features/session";
import { SettingsDialog } from "@/features/settings";
import { usePersistedNumber, usePersistedString } from "@/lib/persisted";
import { useNarrow } from "@/lib/useNarrow";
import { cn } from "@/lib/utils";

/** narrowWidth is where the three panes stop fitting side by side. */
const narrowWidth = 1024;

export function Workbench() {
  const params = useParams();
  const sessionId = params.sessionId ?? "";
  const session = useSession(sessionId === "" ? undefined : sessionId);
  const workspaceId = session.data?.session.workspace_id ?? "";

  const [sidebarWidth, setSidebarWidth] = usePersistedNumber("eika.pane.sidebar", 260);
  const [panelWidth, setPanelWidth] = usePersistedNumber("eika.pane.panel", 340);
  const [activePanel, setActivePanel] = usePersistedString("eika.panel.active", "tree");
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [drawer, setDrawer] = useState<"sidebar" | "panel" | null>(null);
  const narrow = useNarrow(narrowWidth);

  const tabs = availablePanels({ sessionId, workspaceId });
  const panel = tabs.find((tab) => tab.id === activePanel) ?? tabs[0];

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
        {tabs.map((tab) => (
          <button
            key={tab.id}
            type="button"
            role="tab"
            id={`panel-tab-${tab.id}`}
            aria-selected={panel?.id === tab.id}
            aria-controls={`panel-${tab.id}`}
            className={cn(
              "hover:bg-accent/50 focus-visible:ring-ring flex items-center gap-1.5 rounded-sm px-2 py-1 text-xs focus-visible:ring-1 focus-visible:outline-none",
              panel?.id === tab.id && "bg-accent font-medium",
            )}
            onClick={() => {
              setActivePanel(tab.id);
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
        className="min-h-0 flex-1 overflow-y-auto"
      >
        {panel && <panel.Component sessionId={sessionId} workspaceId={workspaceId} />}
      </div>
    </aside>
  );

  const centre =
    sessionId === "" ? (
      <div className="text-muted-foreground flex flex-1 items-center justify-center p-8 text-sm">
        Pick a session on the left, or create one in a workspace.
      </div>
    ) : (
      <SessionView sessionId={sessionId} />
    );

  return (
    <div className="bg-background text-foreground flex h-screen flex-col">
      <header className="flex shrink-0 items-center gap-2 border-b px-2 py-1.5">
        <Terminal aria-hidden className="size-4" />
        <span className="text-sm font-semibold">Eika</span>
        {narrow && (
          <>
            <Button
              size="icon"
              variant="ghost"
              className="size-7"
              aria-label="Show projects"
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
              setSettingsOpen(true);
            }}
          />
          <ThemeToggle />
          <Button
            size="icon"
            variant="ghost"
            className="size-7"
            aria-label="Settings"
            onClick={() => {
              setSettingsOpen(true);
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
              width={panelWidth}
              onWidthChange={setPanelWidth}
              min={240}
              max={640}
              label="Resize the context panels"
              panel={contextPane}
            >
              {centre}
            </ResizableSplit>
          </ResizableSplit>
        </main>
      )}

      <SettingsDialog open={settingsOpen} onOpenChange={setSettingsOpen} />
    </div>
  );
}
