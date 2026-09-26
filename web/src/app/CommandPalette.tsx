/**
 * Cmd/Ctrl+K. It switches sessions and chats, opens a new session in the
 * workspace that is open or a new chat, toggles the theme, and opens the
 * settings. It reads the lists it
 * offers from the same queries the sidebar uses, so it needs no state.
 */

import { Command as CommandIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router";

import { getTheme, nextTheme, setTheme } from "@/app/theme";
import { Button } from "@/components/ui/button";
import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { useSession } from "@/features/session";
import { useChats, useCreateSession, useSessions } from "@/features/sessions";
import { useWorkspaces } from "@/features/workspaces";

export type CommandPaletteProps = {
  /** onOpenSettings opens the settings dialog the workbench owns. */
  onOpenSettings: () => void;
};

export function CommandPalette({ onOpenSettings }: CommandPaletteProps) {
  const [open, setOpen] = useState(false);
  const params = useParams();
  const sessionId = params.sessionId ?? "";
  const session = useSession(sessionId === "" ? undefined : sessionId);
  const workspaceId = session.data?.session.workspace_id;
  const workspaces = useWorkspaces();
  // With no workspace open this lists every session, chats included; the
  // chats have a group of their own, so they are left out of this one.
  const sessions = useSessions(workspaceId, true);
  const workspaceSessions = (sessions.data ?? []).filter((s) => s.workspace_id !== undefined);
  const chats = useChats();
  const createSession = useCreateSession();
  const navigate = useNavigate();

  // A global shortcut has no element to hang off, so it is a document
  // listener that cleans up after itself.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() !== "k" || !(e.metaKey || e.ctrlKey)) return;
      e.preventDefault();
      setOpen((wasOpen) => !wasOpen);
    };
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
    };
  }, []);

  const run = (action: () => void) => {
    setOpen(false);
    action();
  };

  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        className="text-muted-foreground h-7 gap-2 px-2 text-xs"
        aria-keyshortcuts="Meta+K Control+K"
        onClick={() => {
          setOpen(true);
        }}
      >
        Commands
        {/* An icon, not the ⌘ character, which the bundled fonts lack: a
            system font would draw it differently on every machine. */}
        <kbd
          aria-hidden
          className="bg-muted flex items-center gap-0.5 rounded-md px-1 font-mono text-2xs"
        >
          <CommandIcon aria-hidden className="size-3" />K
        </kbd>
      </Button>
      <CommandDialog
        open={open}
        onOpenChange={setOpen}
        title="Commands"
        description="Switch session or chat, create one, or change the theme."
      >
        {/*
          This build's CommandDialog renders the dialog alone; cmdk's own
          context comes from Command, so the list must be wrapped here rather
          than by hand-editing components/ui.
        */}
        <Command>
          <CommandInput placeholder="Type a command or a session title…" />
          <CommandList>
            <CommandEmpty>Nothing matches.</CommandEmpty>
            <CommandGroup heading="Actions">
              {workspaceId !== undefined && (
                <CommandItem
                  onSelect={() => {
                    run(() => {
                      createSession.mutate(
                        { workspace_id: workspaceId },
                        { onSuccess: (created) => void navigate(`/sessions/${created.id}`) },
                      );
                    });
                  }}
                >
                  New session in this workspace
                </CommandItem>
              )}
              <CommandItem
                onSelect={() => {
                  run(() => {
                    createSession.mutate(
                      { chat: true },
                      { onSuccess: (created) => void navigate(`/sessions/${created.id}`) },
                    );
                  });
                }}
              >
                New chat
              </CommandItem>
              <CommandItem
                onSelect={() => {
                  run(() => {
                    setTheme(nextTheme(getTheme()));
                  });
                }}
              >
                Toggle the theme
              </CommandItem>
              <CommandItem
                onSelect={() => {
                  run(onOpenSettings);
                }}
              >
                Open settings
              </CommandItem>
            </CommandGroup>

            {/* The forks and child agents of this workspace's sessions are
                here too: they are sessions to switch to like any other. The
                kind is spelled out, because the palette is a flat list and
                the sidebar's nesting is what says it there. */}
            <CommandGroup heading="Sessions">
              {workspaceSessions.map((entry) => (
                <CommandItem
                  key={entry.id}
                  value={`session ${entry.kind} ${entry.title} ${entry.id}`}
                  onSelect={() => {
                    run(() => void navigate(`/sessions/${entry.id}`));
                  }}
                >
                  {entry.title}
                  {entry.kind !== "user" && (
                    <span className="text-muted-foreground ml-auto font-mono text-xs">
                      {entry.kind}
                    </span>
                  )}
                </CommandItem>
              ))}
            </CommandGroup>

            <CommandGroup heading="Chats">
              {(chats.data ?? []).map((entry) => (
                <CommandItem
                  key={entry.id}
                  value={`chat ${entry.title} ${entry.id}`}
                  onSelect={() => {
                    run(() => void navigate(`/sessions/${entry.id}`));
                  }}
                >
                  {entry.title}
                  <span className="text-muted-foreground ml-auto font-mono text-xs">chat</span>
                </CommandItem>
              ))}
            </CommandGroup>

            <CommandGroup heading="Workspaces">
              {(workspaces.data ?? []).map((workspace) => (
                <CommandItem
                  key={workspace.id}
                  value={`workspace ${workspace.name} ${workspace.branch}`}
                  onSelect={() => {
                    run(() => void navigate(`/workspaces/${workspace.id}`));
                  }}
                >
                  {workspace.name}
                  <span className="text-muted-foreground ml-auto font-mono text-xs">
                    {workspace.state}
                  </span>
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </CommandDialog>
    </>
  );
}
