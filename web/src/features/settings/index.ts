/** The settings feature: the dialog, its tabs, the settings queries, and the system check. */
export { DefaultModelSelect } from "@/features/settings/DefaultModelSelect";
export {
  settingKeys,
  settingNumber,
  settingString,
  setupComplete,
  useSaveSettings,
  useSettings,
  useSystem,
} from "@/features/settings/queries";
export { SettingsDialog } from "@/features/settings/SettingsDialog";
export { useSettingsDialog } from "@/features/settings/store";
export type { SettingsTab } from "@/features/settings/store";
export { SystemCheck } from "@/features/settings/SystemCheck";
