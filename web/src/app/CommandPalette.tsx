/**
 * Cmd/Ctrl+K. It switches chats, opens a new chat, toggles the theme, and
 * opens the settings. It reads the list it offers from the same query the
 * sidebar uses, so it needs no state.
 */

import { Command as CommandIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { useNavigate } from "react-router";

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
import { useChats, useCreateSession } from "@/features/sessions";

export type CommandPaletteProps = {
  /** onOpenSettings opens the settings dialog the workbench owns. */
  onOpenSettings: () => void;
};

export function CommandPalette({ onOpenSettings }: CommandPaletteProps) {
  const [open, setOpen] = useState(false);
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
        description="Switch chat, start one, or change the theme."
      >
        {/*
          This build's CommandDialog renders the dialog alone; cmdk's own
          context comes from Command, so the list must be wrapped here rather
          than by hand-editing components/ui.
        */}
        <Command>
          <CommandInput placeholder="Type a command or a chat title…" />
          <CommandList>
            <CommandEmpty>Nothing matches.</CommandEmpty>
            <CommandGroup heading="Actions">
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
          </CommandList>
        </Command>
      </CommandDialog>
    </>
  );
}
