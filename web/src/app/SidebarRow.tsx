/**
 * The sidebar's rows. Every project, workspace, and session is one `Row`:
 * a label that opens or folds it, a button or two for what is done most,
 * and a menu with everything else, which a right-click opens as well. A row
 * that can be renamed swaps its label for a field in place.
 */

import { Archive, ChevronDown, ChevronRight, MoreHorizontal, Pin } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { useRef, useState } from "react";

import type { Renaming } from "@/app/useRenaming";
import { RenameField } from "@/components/RenameField";
import { Button } from "@/components/ui/button";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuShortcut,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { failureText } from "@/lib/failure";
import { cn } from "@/lib/utils";

/** RowAction is one entry of a row's menu. */
export type RowAction = {
  label: string;
  icon: LucideIcon;
  onSelect: () => void;
  destructive?: boolean;
  disabled?: boolean;
  /** shortcut is the key that does the same on the focused row, such as F2. */
  shortcut?: string;
  /** separated draws a line above the action, setting its group apart. */
  separated?: boolean;
  /**
   * takesFocus keeps the menu from handing focus back to the row when it
   * closes, for an action that puts focus somewhere itself, as renaming does.
   */
  takesFocus?: boolean;
};

type RowProps = {
  /** indent is the label's left padding, in pixels. */
  indent: number;
  icon: React.ReactNode;
  label: string;
  /** labelNote is read after the label by a screen reader only, such as " (fork)". */
  labelNote?: string;
  meta?: string;
  onClick: () => void;
  /** expanded, when set, makes the row fold what is under it and draws its chevron. */
  expanded?: boolean;
  /** current marks the row of the page that is open. */
  current?: boolean;
  pinned?: boolean;
  /** quick are the buttons shown beside the menu, for what is done most. */
  quick?: React.ReactNode;
  /** menu is everything else that can be done to the row. */
  menu: RowAction[];
  /** renaming, with onRename, lets the row be renamed in place. */
  renaming?: Renaming;
  onRename?: (name: string) => void;
};

export function Row({
  indent,
  icon,
  label,
  labelNote,
  meta,
  onClick,
  expanded,
  current = false,
  pinned = false,
  quick,
  menu,
  renaming,
  onRename,
}: RowProps) {
  const chevron =
    expanded === undefined ? null : expanded ? (
      <ChevronDown aria-hidden className="size-3.5 shrink-0" />
    ) : (
      <ChevronRight aria-hidden className="size-3.5 shrink-0" />
    );
  if (renaming?.active === true && onRename !== undefined) {
    return (
      <div
        className="bg-accent flex items-center gap-1.5 rounded-md py-0.5 pr-1"
        style={{ paddingLeft: `${String(indent)}px` }}
      >
        {chevron}
        {icon}
        <RenameField
          value={label}
          label={`Rename ${label}`}
          onSubmit={onRename}
          onDone={renaming.stop}
        />
      </div>
    );
  }
  const canRename = renaming !== undefined && onRename !== undefined;
  return (
    <RowContextMenu label={label} actions={menu}>
      <div
        className={cn(
          "group hover:bg-accent flex items-center gap-1 rounded-md pr-1 transition-colors",
          current && "bg-accent",
        )}
      >
        <button
          ref={renaming?.labelRef}
          type="button"
          aria-expanded={expanded}
          aria-current={current ? "page" : undefined}
          aria-keyshortcuts={canRename ? "F2" : undefined}
          onClick={onClick}
          onKeyDown={(e) => {
            if (e.key === "F2" && canRename) {
              e.preventDefault();
              renaming.start();
            }
          }}
          style={{ paddingLeft: `${String(indent)}px` }}
          className="focus-visible:ring-ring flex min-w-0 flex-1 items-center gap-1.5 py-1 text-left text-xs focus-visible:ring-1 focus-visible:outline-none"
        >
          {chevron}
          {icon}
          {/* A row that folds others is a heading of sorts; a session is not. */}
          <span className={cn("truncate", expanded !== undefined && "font-medium")}>{label}</span>
          {labelNote !== undefined && <span className="sr-only">{labelNote}</span>}
          {pinned && (
            <>
              <Pin aria-hidden className="text-muted-foreground size-3 shrink-0" />
              <span className="sr-only"> (pinned)</span>
            </>
          )}
          {/* The meta starts from no width and takes what the label leaves,
              so a long branch never squeezes the name to a letter. */}
          {meta !== undefined && meta !== "" && (
            <span className="text-muted-foreground min-w-0 flex-1 truncate font-mono text-2xs">
              {meta}
            </span>
          )}
        </button>
        {/* The buttons show on hover and focus, and stay while the menu they
            opened is open, so the menu never hangs off nothing. */}
        <span className="flex shrink-0 gap-0.5 opacity-0 group-focus-within:opacity-100 group-hover:opacity-100 has-[[data-state=open]]:opacity-100">
          {quick}
          <RowMenu label={label} actions={menu} />
        </span>
      </div>
    </RowContextMenu>
  );
}

/**
 * RowMenu is a row's actions behind a button of its own, which is how the
 * keyboard and a touch screen reach them.
 */
function RowMenu({ label, actions }: { label: string; actions: RowAction[] }) {
  const keepFocus = useRef(false);
  return (
    // Not modal: an action that opens a dialog must not wait for the menu
    // to hand the page back first.
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger asChild>
        <Button size="icon-xs" variant="ghost" aria-label={`Actions for ${label}`} title="Actions">
          <MoreHorizontal aria-hidden className="size-3" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="end"
        className="min-w-40"
        onCloseAutoFocus={(e) => {
          if (keepFocus.current) e.preventDefault();
          keepFocus.current = false;
        }}
      >
        {actions.map((action) => [
          action.separated === true && <DropdownMenuSeparator key={`${action.label}-line`} />,
          <DropdownMenuItem
            key={action.label}
            variant={action.destructive === true ? "destructive" : "default"}
            disabled={action.disabled}
            aria-keyshortcuts={action.shortcut}
            onSelect={() => {
              keepFocus.current = action.takesFocus === true;
              action.onSelect();
            }}
          >
            <action.icon aria-hidden className="size-3.5" />
            {action.label}
            {action.shortcut !== undefined && (
              <DropdownMenuShortcut aria-hidden className="font-mono">
                {action.shortcut}
              </DropdownMenuShortcut>
            )}
          </DropdownMenuItem>,
        ])}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** RowContextMenu opens a row's actions where it is right-clicked or long-pressed. */
function RowContextMenu({
  label,
  actions,
  children,
}: {
  label: string;
  actions: RowAction[];
  children: React.ReactNode;
}) {
  const keepFocus = useRef(false);
  return (
    <ContextMenu modal={false}>
      <ContextMenuTrigger asChild>{children}</ContextMenuTrigger>
      <ContextMenuContent
        aria-label={`Actions for ${label}`}
        className="min-w-40"
        onCloseAutoFocus={(e) => {
          if (keepFocus.current) e.preventDefault();
          keepFocus.current = false;
        }}
      >
        {actions.map((action) => [
          action.separated === true && <ContextMenuSeparator key={`${action.label}-line`} />,
          <ContextMenuItem
            key={action.label}
            variant={action.destructive === true ? "destructive" : "default"}
            disabled={action.disabled}
            aria-keyshortcuts={action.shortcut}
            onSelect={() => {
              keepFocus.current = action.takesFocus === true;
              action.onSelect();
            }}
          >
            <action.icon aria-hidden className="size-3.5" />
            {action.label}
            {action.shortcut !== undefined && (
              <ContextMenuShortcut aria-hidden className="font-mono">
                {action.shortcut}
              </ContextMenuShortcut>
            )}
          </ContextMenuItem>,
        ])}
      </ContextMenuContent>
    </ContextMenu>
  );
}

type IconButtonProps = {
  label: string;
  children: React.ReactNode;
  onClick: () => void;
  disabled?: boolean;
};

/** IconButton is one of a row's quick buttons. */
export function IconButton({ label, children, onClick, disabled }: IconButtonProps) {
  return (
    <Button
      size="icon-xs"
      variant="ghost"
      aria-label={label}
      title={label}
      disabled={disabled}
      onClick={onClick}
    >
      {children}
    </Button>
  );
}

type ArchivedGroupProps = {
  count: number;
  /** indent is the left padding of the heading, in pixels, like the rows beside it. */
  indent: number;
  /** what names what is archived, such as "sessions", for a screen reader. */
  what: string;
  children: React.ReactNode;
};

/**
 * ArchivedGroup is the folded heading under a list that holds what was
 * archived from it. Archived things are set aside, not gone: they are one
 * click away, under the list they came from.
 */
export function ArchivedGroup({ count, indent, what, children }: ArchivedGroupProps) {
  const [open, setOpen] = useState(false);
  if (count === 0) return null;
  return (
    <li>
      <button
        type="button"
        aria-expanded={open}
        aria-label={`Archived ${what} (${String(count)})`}
        onClick={() => {
          setOpen((was) => !was);
        }}
        style={{ paddingLeft: `${String(indent)}px` }}
        className="text-muted-foreground hover:bg-accent focus-visible:ring-ring flex w-full items-center gap-1.5 rounded-md py-1 text-left text-xs transition-colors focus-visible:ring-1 focus-visible:outline-none"
      >
        {open ? (
          <ChevronDown aria-hidden className="size-3.5 shrink-0" />
        ) : (
          <ChevronRight aria-hidden className="size-3.5 shrink-0" />
        )}
        <Archive aria-hidden className="size-3.5 shrink-0" />
        <span>Archived</span>
        <span className="font-mono text-2xs">{count}</span>
      </button>
      {open && <ul>{children}</ul>}
    </li>
  );
}

type RowFailureProps = {
  indent: number;
  /** action is what failed, as `failureText` words it: "rename the session". */
  action: string;
  error: Error | null;
};

/** RowFailure says, under the row, which of its actions failed and why. */
export function RowFailure({ indent, action, error }: RowFailureProps) {
  if (error === null) return null;
  return (
    <p
      role="alert"
      className="text-destructive py-0.5 pr-1 text-xs"
      style={{ paddingLeft: `${String(indent)}px` }}
    >
      {failureText(action, error)}
    </p>
  );
}
