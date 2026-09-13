/**
 * Deployment settings: the model a run uses when none is named, the colour
 * scheme, and the token this browser holds.
 */

import { useSyncExternalStore } from "react";

import { disconnect, getConnection, subscribeConnection } from "@/api/connection";
import { Button } from "@/components/ui/button";
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
import { Separator } from "@/components/ui/separator";
import { getTheme, setTheme, subscribeTheme } from "@/app/theme";
import type { Theme } from "@/app/theme";
import {
  defaultModelKey,
  defaultModelOf,
  useModels,
  useSaveSettings,
  useSettings,
} from "@/features/settings/queries";

export type SettingsDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
};

const themeLabels: Record<Theme, string> = {
  light: "Light",
  dark: "Dark",
  system: "Follow the system",
};

export function SettingsDialog({ open, onOpenChange }: SettingsDialogProps) {
  const settings = useSettings();
  const models = useModels();
  const save = useSaveSettings();
  const theme = useSyncExternalStore<Theme>(subscribeTheme, getTheme, () => "system");
  const connection = useSyncExternalStore(subscribeConnection, getConnection, () => ({
    baseUrl: "",
    token: "",
  }));

  const defaultModel = defaultModelOf(settings.data);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Settings</DialogTitle>
          <DialogDescription>
            The model setting is stored in the harness; the theme and the token belong to this
            browser.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-2">
          <Label htmlFor="default-model">Default model</Label>
          <Select
            value={defaultModel}
            onValueChange={(value) => {
              save.mutate({ [defaultModelKey]: value });
            }}
          >
            <SelectTrigger id="default-model" className="w-full">
              <SelectValue placeholder="the first configured model" />
            </SelectTrigger>
            <SelectContent>
              {(models.data?.models ?? []).map((model) => (
                <SelectItem key={model.name} value={model.name}>
                  {model.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <p className="text-muted-foreground text-xs">
            Used when a message names no model. Configure the list in the deployment's
            <code className="mx-1">eika.yaml</code>.
          </p>
          {save.isError && (
            <p role="alert" className="text-destructive text-xs">
              {save.error.message}
            </p>
          )}
        </div>

        <Separator />

        <div className="space-y-2">
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
        </div>

        <Separator />

        <div className="space-y-2">
          <h3 className="text-sm font-medium">Connection</h3>
          <dl className="text-muted-foreground grid grid-cols-[auto_1fr] gap-x-3 font-mono text-xs">
            <dt>harness</dt>
            <dd className="truncate">{connection.baseUrl || "this origin"}</dd>
            <dt>token</dt>
            <dd>{connection.token === "" ? "none" : "stored in this browser"}</dd>
          </dl>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => {
              disconnect();
              onOpenChange(false);
            }}
          >
            Forget the token
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
