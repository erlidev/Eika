/** The workspaces feature: creation, lifecycle actions, and the state badge. */
export { CreateWorkspaceDialog } from "@/features/workspaces/CreateWorkspaceDialog";
export {
  useCreateWorkspace,
  useDeleteWorkspace,
  useWorkspace,
  useWorkspaceAction,
  useWorkspaces,
} from "@/features/workspaces/queries";
export type { WorkspaceAction } from "@/features/workspaces/queries";
export { useWorkspaceEvents } from "@/features/workspaces/useWorkspaceEvents";
export { WorkspaceStateBadge } from "@/features/workspaces/WorkspaceStateBadge";
