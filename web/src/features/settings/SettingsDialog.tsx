/**
 * The settings: models and providers, the harness-wide choices, web search,
 * the account, and how the app looks. Everything but the appearance is stored
 * in the harness. Its open state is `store.ts`, so anything can open it on a
 * tab.
 *
 * The dialog is one fixed size whatever tab is showing, and each tab scrolls
 * inside it: five tabs of very different lengths would otherwise resize the
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
import { ModelsPanel } from "@/features/providers";
import { useTranscriptPreferences } from "@/features/session";
import type { ReasoningDisplay } from "@/features/session";
import { AccountSettings } from "@/features/settings/AccountSettings";
import { GeneralSettings } from "@/features/settings/GeneralSettings";
import { settingKeys, useSaveSettings } from "@/features/settings/queries";
import { SearchSettings } from "@/features/settings/SearchSettings";
import { useSettingsDialog } from "@/features/settings/store";
import type { SettingsTab } from "@/features/settings/store";
import { Switch } from "@/components/ui/switch";
import { useNarrow } from "@/lib/useNarrow";

/** sideNavWidth is where the tab list stops fitting beside the panel. */
const sideNavWidth = 640;

const tabs: readonly { value: SettingsTab; label: string }[] = [
  { value: "models", label: "Models" },
  { value: "general", label: "General" },
  { value: "search", label: "Search" },
  { value: "account", label: "Account" },
  { value: "appearance", label: "Appearance" },
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
  const save = useSaveSettings();
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
              <ModelsPanel
                onDefaultChange={(name) => {
                  save.mutate({ [settingKeys.defaultModel]: name });
                }}
                defaultError={save.isError ? save.error : undefined}
              />
            </TabsContent>
            <TabsContent value="general">
              <GeneralSettings />
            </TabsContent>
            <TabsContent value="search">
              <SearchSettings />
            </TabsContent>
            <TabsContent value="account">
              <AccountSettings />
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
  return (
    <div className="space-y-6">
      <ThemeSelect />
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
