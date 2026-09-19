/**
 * The settings: models and providers, the harness-wide choices, web search,
 * the account, and the colour scheme. Everything but the colour scheme is stored in the
 * harness. Its open state is `store.ts`, so anything can open it on a tab.
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
import { AccountSettings } from "@/features/settings/AccountSettings";
import { GeneralSettings } from "@/features/settings/GeneralSettings";
import { settingKeys, useSaveSettings } from "@/features/settings/queries";
import { SearchSettings } from "@/features/settings/SearchSettings";
import { useSettingsDialog } from "@/features/settings/store";
import type { SettingsTab } from "@/features/settings/store";

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

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>Settings</DialogTitle>
          <DialogDescription>
            Stored in the harness and shared by every browser, except the theme.
          </DialogDescription>
        </DialogHeader>
        <Tabs
          value={tab}
          onValueChange={(value) => {
            setTab(value as SettingsTab);
          }}
        >
          <TabsList>
            {tabs.map((option) => (
              <TabsTrigger key={option.value} value={option.value}>
                {option.label}
              </TabsTrigger>
            ))}
          </TabsList>
          <TabsContent value="models" className="pt-3">
            <ModelsPanel
              onDefaultChange={(name) => {
                save.mutate({ [settingKeys.defaultModel]: name });
              }}
              defaultError={save.isError ? save.error.message : undefined}
            />
          </TabsContent>
          <TabsContent value="general" className="pt-3">
            <GeneralSettings />
          </TabsContent>
          <TabsContent value="search" className="pt-3">
            <SearchSettings />
          </TabsContent>
          <TabsContent value="account" className="pt-3">
            <AccountSettings />
          </TabsContent>
          <TabsContent value="appearance" className="pt-3">
            <ThemeSelect />
          </TabsContent>
        </Tabs>
      </DialogContent>
    </Dialog>
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
