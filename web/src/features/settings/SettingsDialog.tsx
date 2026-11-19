/**
 * The settings: models and providers, agents, the user's instructions, the harness-wide choices,
 * compaction, the computer's limits and network, web search,
 * MCP servers, the account, and how the app looks. Everything but the appearance is stored
 * in the harness. Its open state is `store.ts`, so anything can open it on a
 * tab.
 *
 * The dialog is one fixed size whatever tab is showing, and each tab scrolls
 * inside it: eleven tabs of very different lengths would otherwise resize the
 * window under the pointer every time one was picked.
 */

import { useSyncExternalStore } from "react";

import { getTheme, setTheme, subscribeTheme } from "@/app/theme";
import type { Theme } from "@/app/theme";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { MCPSettings } from "@/features/mcp";
import { AgentsSettings } from "@/features/agents";
import { ModelsPanel } from "@/features/providers";
import { useTranscriptPreferences } from "@/features/session";
import type { ComposerInput, ComposerMode, ReasoningDisplay } from "@/features/session";
import { AccountSettings } from "@/features/settings/AccountSettings";
import { CompactionSettings } from "@/features/settings/CompactionSettings";
import { ComputerSettings } from "@/features/settings/ComputerSettings";
import { DeveloperSettings } from "@/features/settings/DeveloperSettings";
import { NotificationsSettings } from "@/features/settings/NotificationsSettings";
import { GeneralSettings } from "@/features/settings/GeneralSettings";
import { InstructionsSettings } from "@/features/settings/InstructionsSettings";
import { SearchSettings } from "@/features/settings/SearchSettings";
import { useSettingsDialog } from "@/features/settings/store";
import type { SettingsTab } from "@/features/settings/store";
import { Switch } from "@/components/ui/switch";
import { useNarrow } from "@/lib/useNarrow";

/** sideNavWidth is where the tab list stops fitting beside the panel. */
const sideNavWidth = 640;

const tabs: readonly { value: SettingsTab; label: string }[] = [
  { value: "models", label: "Models" },
  { value: "agents", label: "Agents" },
  { value: "instructions", label: "Instructions" },
  { value: "general", label: "General" },
  { value: "compaction", label: "Compaction" },
  { value: "computer", label: "Computer" },
  { value: "search", label: "Search" },
  { value: "mcp", label: "MCP" },
  { value: "notifications", label: "Notifications" },
  { value: "account", label: "Account" },
  { value: "appearance", label: "Appearance" },
  { value: "developer", label: "Developer" },
];

const themeLabels: Record<Theme, string> = {
  light: "Light",
  dark: "Dark",
  system: "Follow the system",
};

export function SettingsDialog() {
  const open = useSettingsDialog((s) => s.open);
  const tab = useSettingsDialog((s) => s.tab);
  const setOpen = useSettingsDialog((s) => s.setOpen);
  const setTab = useSettingsDialog((s) => s.setTab);
  // A phone has no room for a column of tab names beside the panel, so there
  // the list goes back above it. It is the same list either way.
  const narrow = useNarrow(sideNavWidth);

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      {/* One height for every tab, and the panel scrolls inside it. gap-0 and
          p-0 replace the dialog's own padding, which the regions set for
          themselves. */}
      <DialogContent className="flex h-[min(85vh,42rem)] flex-col gap-0 p-0 sm:max-w-3xl">
        <DialogHeader className="shrink-0 border-b px-4 py-3">
          <DialogTitle>Settings</DialogTitle>
          <DialogDescription>
            Stored in the harness and shared by every browser, except the appearance.
          </DialogDescription>
        </DialogHeader>
        {/* min-w-0: the dialog is a flex column, and without it the tab row's
            width would widen every tab past a phone's screen. */}
        <Tabs
          orientation={narrow ? "horizontal" : "vertical"}
          className="min-h-0 min-w-0 flex-1 gap-0"
          value={tab}
          onValueChange={(value) => {
            setTab(value as SettingsTab);
          }}
        >
          <TabsList
            variant="line"
            className={
              narrow
                ? "h-auto max-w-full shrink-0 justify-start gap-1 overflow-x-auto border-b px-2 py-1.5"
                : // self-stretch rather than h-full: the list primitive sets
                  // its own height for a vertical list, and the two would fight.
                  "w-40 shrink-0 justify-start gap-0.5 self-stretch overflow-y-auto border-r p-2"
            }
          >
            {tabs.map((option) => (
              <TabsTrigger key={option.value} value={option.value} className="flex-none">
                {option.label}
              </TabsTrigger>
            ))}
          </TabsList>
          <div className="min-h-0 min-w-0 flex-1 overflow-y-auto p-4">
            <TabsContent value="models">
              <ModelsPanel />
            </TabsContent>
            <TabsContent value="agents">
              <AgentsSettings />
            </TabsContent>
            <TabsContent value="instructions">
              <InstructionsSettings />
            </TabsContent>
            <TabsContent value="general">
              <GeneralSettings />
            </TabsContent>
            <TabsContent value="compaction">
              <CompactionSettings />
            </TabsContent>
            <TabsContent value="computer">
              <ComputerSettings />
            </TabsContent>
            <TabsContent value="search">
              <SearchSettings />
            </TabsContent>
            <TabsContent value="mcp">
              <MCPSettings />
            </TabsContent>
            <TabsContent value="notifications">
              <NotificationsSettings />
            </TabsContent>
            <TabsContent value="account">
              <AccountSettings />
            </TabsContent>
            <TabsContent value="developer">
              <DeveloperSettings />
            </TabsContent>
            <TabsContent value="appearance">
              <AppearanceSettings />
            </TabsContent>
          </div>
        </Tabs>
      </DialogContent>
    </Dialog>
  );
}

/** AppearanceSettings are the choices this browser remembers on its own. */
function AppearanceSettings() {
  const reasoning = useTranscriptPreferences((s) => s.reasoning);
  const setReasoning = useTranscriptPreferences((s) => s.setReasoning);
  const speed = useTranscriptPreferences((s) => s.speed);
  const setSpeed = useTranscriptPreferences((s) => s.setSpeed);
  const input = useTranscriptPreferences((s) => s.input);
  const setInput = useTranscriptPreferences((s) => s.setInput);
  const composer = useTranscriptPreferences((s) => s.composer);
  const setComposer = useTranscriptPreferences((s) => s.setComposer);
  return (
    <div className="space-y-6">
      <ThemeSelect />
      <div className="space-y-2 rounded-md border p-3">
        <Label htmlFor="composer-input">Message input</Label>
        <p className="text-muted-foreground text-xs">
          Choose touch or keyboard input, or follow the primary pointer.
        </p>
        <Select
          value={input}
          onValueChange={(value: ComposerInput) => {
            setInput(value);
          }}
        >
          <SelectTrigger id="composer-input" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="auto">Follow the pointer</SelectItem>
            <SelectItem value="touch">Touch</SelectItem>
            <SelectItem value="keyboard">Keyboard</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <div className="flex items-start justify-between gap-4 rounded-md border p-3">
        <div className="space-y-0.5">
          <Label htmlFor="reasoning-display" className="text-sm">
            Show the model&apos;s thinking in full
          </Label>
          <p className="text-muted-foreground text-xs">
            Reasoning is streamed either way. Off, each block is one line in the transcript that
            opens on a click; on, every block starts open.
          </p>
        </div>
        <Switch
          id="reasoning-display"
          checked={reasoning === "expanded"}
          onCheckedChange={(on) => {
            setReasoning((on ? "expanded" : "compact") satisfies ReasoningDisplay);
          }}
        />
      </div>
      <div className="flex items-start justify-between gap-4 rounded-md border p-3">
        <div className="space-y-0.5">
          <Label htmlFor="transcript-speed" className="text-sm">
            Show how fast each answer was produced
          </Label>
          <p className="text-muted-foreground text-xs">
            A line of tokens per second under every answer: generation always, and prompt processing
            for an endpoint that measures its own. llama.cpp, vLLM, TabbyAPI, Groq, LM Studio and
            NIM report both; for the rest the generation rate is timed here, from the first token,
            and there is no prompt rate to state.
          </p>
        </div>
        <Switch id="transcript-speed" checked={speed} onCheckedChange={setSpeed} />
      </div>
      <div className="flex items-start justify-between gap-4 rounded-md border p-3">
        <div className="space-y-0.5">
          <Label htmlFor="composer-markdown" className="text-sm">
            Edit messages as markdown
          </Label>
          <p className="text-muted-foreground text-xs">
            For keyboard input, the message box behaves like an editor: Enter starts a new line and
            carries a list or quotation on, Tab and Shift+Tab nest a list item or indent code,
            typing ``` closes the code block, and Ctrl+Enter sends. Off, Enter sends and Shift+Enter
            starts a new line.
          </p>
        </div>
        <Switch
          id="composer-markdown"
          checked={composer === "markdown"}
          onCheckedChange={(on) => {
            setComposer((on ? "markdown" : "chat") satisfies ComposerMode);
          }}
        />
      </div>
    </div>
  );
}

function ThemeSelect() {
  const theme = useSyncExternalStore<Theme>(subscribeTheme, getTheme, () => "system");
  return (
    <div className="space-y-1.5">
      <Label htmlFor="theme">Theme</Label>
      <Select
        value={theme}
        onValueChange={(value) => {
          setTheme(value as Theme);
        }}
      >
        <SelectTrigger id="theme" className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {(Object.keys(themeLabels) as Theme[]).map((option) => (
            <SelectItem key={option} value={option}>
              {themeLabels[option]}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <p className="text-muted-foreground text-xs">Remembered by this browser only.</p>
    </div>
  );
}
