/** The workspaces feature: creation, lifecycle actions, and the state badge. */
export { CreateWorkspaceDialog } from "@/features/workspaces/CreateWorkspaceDialog";
export {
  useDeleteWorkspace,
  useUpdateWorkspace,
  useWorkspace,
  useWorkspaceAction,
  useWorkspaces,
} from "@/features/workspaces/queries";
export { RunningWorkspace } from "@/features/workspaces/RunningWorkspace";
export { useWorkspaceEvents } from "@/features/workspaces/useWorkspaceEvents";
export { WorkspaceStateBadge } from "@/features/workspaces/WorkspaceStateBadge";
