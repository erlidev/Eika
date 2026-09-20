/**
 * The shell session behind the Terminal panel. It outlives the panel: the
 * context pane unmounts a tab the user leaves, and a shell must keep running
 * (and keep its screen) until the user moves to another workspace. So the
 * xterm instance and its socket live here, one at a time, and the panel only
 * attaches the terminal's element to its own while it is shown.
 */

import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import type { ITheme } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";

import { subscribeTheme } from "@/app/theme";
import { binaryStringBytes, TerminalSocket } from "@/api/terminal";
import type { TerminalStatus } from "@/api/terminal";
import { themeColor } from "@/lib/themeColor";

/** ShellState is what the panel shows around the terminal. */
export type ShellState = {
  status: TerminalStatus;
  /** exitCode is the shell's exit status once it has exited. */
  exitCode?: number;
};

/** fontFamily is the bundled mono face; the terminal measures it before it opens. */
const fontFamily = '"JetBrains Mono Variable", ui-monospace, monospace';
const fontSize = 12;

function terminalTheme(): ITheme {
  const background = themeColor("background");
  const foreground = themeColor("foreground");
  return {
    background,
    foreground,
    cursor: foreground,
    cursorAccent: background,
    selectionBackground: themeColor("accent"),
    selectionForeground: themeColor("accent-foreground"),
  };
}

/** ShellSession is one workspace's shell and the terminal that draws it. */
export class ShellSession {
  readonly workspaceId: string;
  private readonly host: HTMLDivElement;
  private terminal: Terminal | undefined;
  private fit: FitAddon | undefined;
  private socket: TerminalSocket | undefined;
  private state: ShellState = { status: "connecting" };
  private readonly listeners = new Set<() => void>();
  private stopTheme: (() => void) | undefined;
  private disposed = false;

  constructor(workspaceId: string) {
    this.workspaceId = workspaceId;
    this.host = document.createElement("div");
    this.host.className = "h-full w-full";
  }

  /** getState reports the shell's state; it is a stable object until the state changes. */
  getState = (): ShellState => this.state;

  /** subscribe registers a listener for state changes and returns its unsubscribe. */
  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  /**
   * attach shows the terminal inside container. The first attach opens the
   * terminal and the shell; later ones move the same screen back into view.
   */
  async attach(container: HTMLElement): Promise<void> {
    container.appendChild(this.host);
    if (this.terminal) {
      this.resize();
      return;
    }
    // xterm measures a cell once, when it opens, so the font must be ready.
    await document.fonts.load(`${String(fontSize)}px ${fontFamily}`);
    // Another attach may have opened it, or a dispose ended it, meanwhile.
    if (this.disposed || this.isOpen()) return;
    const terminal = new Terminal({
      fontFamily,
      fontSize,
      cursorBlink: false,
      scrollback: 5000,
      theme: terminalTheme(),
    });
    const fit = new FitAddon();
    terminal.loadAddon(fit);
    terminal.open(this.host);
    this.terminal = terminal;
    this.fit = fit;
    fit.fit();
    terminal.onData((text) => {
      this.socket?.input(text);
    });
    terminal.onBinary((text) => {
      this.socket?.inputBytes(binaryStringBytes(text));
    });
    terminal.onResize(({ rows, cols }) => {
      this.socket?.resize(rows, cols);
    });
    this.stopTheme = subscribeTheme(() => {
      terminal.options.theme = terminalTheme();
    });
    this.connect();
  }

  private isOpen(): boolean {
    return this.terminal !== undefined;
  }

  /** detach takes the terminal out of view; the shell keeps running. */
  detach(): void {
    this.host.remove();
  }

  /** resize fits the terminal to its container, which tells the shell its new size. */
  resize(): void {
    if (!this.host.isConnected || this.host.clientWidth === 0) return;
    this.fit?.fit();
  }

  /** focus puts the keyboard in the terminal. */
  focus(): void {
    this.terminal?.focus();
  }

  /** restart starts a new shell on a clean screen, after an exit or a lost connection. */
  restart(): void {
    this.socket?.close();
    this.terminal?.reset();
    this.connect();
    this.focus();
  }

  /** dispose ends the shell and frees the terminal. */
  dispose(): void {
    this.disposed = true;
    this.socket?.close();
    this.socket = undefined;
    this.stopTheme?.();
    this.terminal?.dispose();
    this.terminal = undefined;
    this.host.remove();
    this.listeners.clear();
  }

  private connect(): void {
    const terminal = this.terminal;
    if (!terminal) return;
    this.setState({ status: "connecting" });
    const socket: TerminalSocket = new TerminalSocket(
      this.workspaceId,
      terminal.rows,
      terminal.cols,
      {
        output: (bytes) => {
          terminal.write(bytes);
        },
        status: (status, exitCode) => {
          // A socket replaced by a restart may still report its close.
          if (this.socket !== socket) return;
          if (status === "exited") {
            terminal.write(`\r\n[process exited ${String(exitCode ?? 0)}]\r\n`);
            this.setState({ status, exitCode: exitCode ?? 0 });
            return;
          }
          this.setState({ status });
        },
      },
    );
    this.socket = socket;
  }

  private setState(next: ShellState): void {
    this.state = next;
    for (const listener of this.listeners) listener();
  }
}

/** current is the one live shell; opening another workspace's ends it. */
let current: ShellSession | undefined;

/**
 * shellSession returns the shell of a workspace, creating it on first use and
 * ending the shell of any other workspace, so at most one runs at a time.
 */
export function shellSession(workspaceId: string): ShellSession {
  if (current?.workspaceId === workspaceId) return current;
  current?.dispose();
  current = new ShellSession(workspaceId);
  return current;
}
