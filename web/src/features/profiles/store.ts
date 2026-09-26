/**
 * Which configuration editor is open, and on which tab. It is a store because
 * more than its own button opens an editor: the Context inspector's Edit
 * links open the session's editor, or the profile a value came from, at the
 * tab that holds it.
 */

import { create } from "zustand";

import type { EditorSection } from "@/features/profiles/form";
import { useSettingsDialog } from "@/features/settings/store";

/** EditorTarget is an editor to open and the tab to open it on. */
type EditorTarget = { id: string; section: EditorSection };

type ConfigEditorState = {
  /** session is the session whose editor is open, if one is. */
  session: EditorTarget | undefined;
  /** profile is the profile the Profiles tab should show next, if one is asked for. */
  profile: EditorTarget | undefined;
  /** editSession opens a session's editor on a tab. */
  editSession: (sessionId: string, section: EditorSection) => void;
  closeSession: () => void;
  /** editProfile opens the settings on the Profiles tab, showing one profile on a tab. */
  editProfile: (profileId: string, section: EditorSection) => void;
  /** takeProfile hands the asked-for profile to the Profiles tab once, and forgets it. */
  takeProfile: () => EditorTarget | undefined;
};

/** useConfigEditor is which configuration editor is open. */
export const useConfigEditor = create<ConfigEditorState>((set, get) => ({
  session: undefined,
  profile: undefined,
  editSession: (id, section) => {
    set({ session: { id, section } });
  },
  closeSession: () => {
    set({ session: undefined });
  },
  editProfile: (id, section) => {
    set({ profile: { id, section }, session: undefined });
    useSettingsDialog.getState().show("profiles");
  },
  takeProfile: () => {
    const target = get().profile;
    if (target !== undefined) set({ profile: undefined });
    return target;
  },
}));
