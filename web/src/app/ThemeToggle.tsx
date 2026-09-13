/** The light/dark/system cycle, as one button. */

import { Monitor, Moon, Sun } from "lucide-react";
import { useSyncExternalStore } from "react";

import { getTheme, nextTheme, setTheme, subscribeTheme } from "@/app/theme";
import type { Theme } from "@/app/theme";
import { Button } from "@/components/ui/button";

const labels: Record<Theme, string> = {
  light: "Theme: light",
  dark: "Theme: dark",
  system: "Theme: follows the system",
};

export function ThemeToggle() {
  const theme = useSyncExternalStore<Theme>(subscribeTheme, getTheme, () => "system");
  const Icon = theme === "light" ? Sun : theme === "dark" ? Moon : Monitor;

  return (
    <Button
      size="icon"
      variant="ghost"
      className="size-7"
      aria-label={labels[theme]}
      title={labels[theme]}
      onClick={() => {
        setTheme(nextTheme(theme));
      }}
    >
      <Icon aria-hidden className="size-4" />
    </Button>
  );
}
