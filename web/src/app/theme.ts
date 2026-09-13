/**
 * The colour scheme. It follows `prefers-color-scheme` until the user picks a
 * side, and the choice is remembered in localStorage. Like `api/connection`,
 * this is a plain module with listeners so that React reads it through
 * useSyncExternalStore instead of an effect.
 */

const storageKey = "eika.theme";

/** Theme is what the user chose; `system` follows the operating system. */
export type Theme = "light" | "dark" | "system";

const themes: readonly Theme[] = ["light", "dark", "system"];

function stored(): Theme {
  try {
    const value = localStorage.getItem(storageKey);
    return themes.includes(value as Theme) ? (value as Theme) : "system";
  } catch {
    return "system";
  }
}

let current: Theme = stored();
const listeners = new Set<() => void>();

/** prefersDark reports what the operating system asks for. */
function prefersDark(): boolean {
  return typeof window !== "undefined" && window.matchMedia("(prefers-color-scheme: dark)").matches;
}

/** getTheme returns the user's choice. */
export function getTheme(): Theme {
  return current;
}

/** resolvedTheme is the scheme actually in force. */
export function resolvedTheme(): "light" | "dark" {
  if (current !== "system") return current;
  return prefersDark() ? "dark" : "light";
}

/** subscribeTheme registers a listener and returns its unsubscribe. */
export function subscribeTheme(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/** applyTheme puts the resolved scheme on the document element. */
function applyTheme(): void {
  const root = document.documentElement;
  const dark = resolvedTheme() === "dark";
  root.classList.toggle("dark", dark);
  root.style.colorScheme = dark ? "dark" : "light";
}

/** setTheme records a choice and applies it. */
export function setTheme(theme: Theme): void {
  current = theme;
  try {
    localStorage.setItem(storageKey, theme);
  } catch {
    // The choice lasts for this page only.
  }
  applyTheme();
  for (const listener of listeners) listener();
}

/** nextTheme is the choice the toggle moves to, cycling light, dark, system. */
export function nextTheme(theme: Theme): Theme {
  switch (theme) {
    case "light":
      return "dark";
    case "dark":
      return "system";
    default:
      return "light";
  }
}

/**
 * startTheme applies the stored choice and keeps following the system while
 * the choice is `system`. It is called once, from `main.tsx`.
 */
export function startTheme(): void {
  applyTheme();
  window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    if (current === "system") {
      applyTheme();
      for (const listener of listeners) listener();
    }
  });
}
