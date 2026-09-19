/**
 * Whether the settings dialog is open, and on which tab. It is a store rather
 * than workbench state because more than the workbench opens it: the command
 * palette, and the notice a session shows when no model is configured.
 */

import { create } from "zustand";

/** SettingsTab names one tab of the settings dialog. */
export type SettingsTab = "models" | "general" | "account" | "appearance";

type SettingsDialogState = {
  open: boolean;
  tab: SettingsTab;
  /** show opens the dialog, on the given tab or the one it was last on. */
  show: (tab?: SettingsTab) => void;
  /** setOpen follows the dialog's own open state, so Esc and the close button work. */
  setOpen: (open: boolean) => void;
  setTab: (tab: SettingsTab) => void;
};

/** useSettingsDialog is the settings dialog's open state. */
export const useSettingsDialog = create<SettingsDialogState>((set) => ({
  open: false,
  tab: "models",
  show: (tab) => {
    set((state) => ({ open: true, tab: tab ?? state.tab }));
  },
  setOpen: (open) => {
    set({ open });
  },
  setTab: (tab) => {
    set({ tab });
  },
}));
