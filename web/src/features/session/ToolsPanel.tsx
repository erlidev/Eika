/**
 * The tools panel of a chat: which tools the model may use, a switch for
 * each, the MCP servers' under their server, and the tools a chat never has. It is how a chat reads as a chat
 * beside the transcript: where a workspace session has its files, terminal,
 * and changes, a chat has this.
 */

import type { Tool } from "@/api/types";
import { ActionError, LoadError, Notice } from "@/components/Notice";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { useSession, useSetSessionTools, useTools } from "@/features/session/queries";
import { chatTools, toolSummary, withTool } from "@/features/session/tools";

export type ToolsPanelProps = {
  sessionId: string;
};

export function ToolsPanel({ sessionId }: ToolsPanelProps) {
  const session = useSession(sessionId);
  const tools = useTools();
  const setTools = useSetSessionTools(sessionId);

  if (session.isPending || tools.isPending) {
    return (
      <div className="p-3">
        <Notice tone="pending">Loading the tools…</Notice>
      </div>
    );
  }
  if (tools.isError) {
    return (
      <div className="p-3">
        <LoadError
          what="the tools"
          error={tools.error}
          retrying={tools.isFetching}
          retry={() => void tools.refetch()}
        />
      </div>
    );
  }
  if (session.isError) {
    return (
      <div className="p-3">
        <LoadError
          what="the chat"
          error={session.error}
          retrying={session.isFetching}
          retry={() => void session.refetch()}
        />
      </div>
    );
  }

  // While a change is on its way the switches show it, so a click is not
  // undone on screen by the value the harness has not answered yet.
  const on = setTools.isPending ? setTools.variables : session.data.session.tools;
  const { offered, servers, unavailable, unavailableServers } = chatTools(tools.data);
  const toggle = (name: string, next: boolean) => {
    setTools.mutate(withTool(on, name, next));
  };

  return (
    <div className="space-y-4 p-3 text-xs">
      <p className="text-muted-foreground">
        A chat has no workspace, so the model can use only the tools below. A change applies from
        the next message.
      </p>

      <ul className="space-y-1" aria-label="Tools in this chat">
        {offered.map((tool) => (
          <ToolSwitch
            key={tool.name}
            tool={tool}
            label={tool.name}
            on={on.includes(tool.name)}
            busy={setTools.isPending}
            onChange={toggle}
          />
        ))}
      </ul>
      {servers.map(({ server, tools: list }) => (
        <section key={server} className="space-y-1">
          <h3 className="text-muted-foreground text-2xs font-semibold tracking-wide uppercase">
            MCP server <span className="font-mono normal-case">{server}</span>
          </h3>
          <ul className="space-y-1" aria-label={`Tools of ${server}`}>
            {list.map((tool) => (
              <ToolSwitch
                key={tool.name}
                tool={tool}
                label={tool.name.replace(`mcp__${server}__`, "")}
                on={on.includes(tool.name)}
                busy={setTools.isPending}
                onChange={toggle}
              />
            ))}
          </ul>
        </section>
      ))}
      {on.length === 0 && (
        <p className="text-muted-foreground">
          Every tool is off: the model answers from what it knows.
        </p>
      )}
      {setTools.isError && <ActionError action="change the tools" error={setTools.error} />}

      {(unavailable.length > 0 || unavailableServers.length > 0) && (
        <section>
          <h3 className="text-muted-foreground mb-1 text-2xs font-semibold tracking-wide uppercase">
            Need a workspace
          </h3>
          {unavailable.length > 0 && (
            <p className="text-muted-foreground">
              Files, commands, and child agents belong to a workspace session:{" "}
              <span className="font-mono">{unavailable.join(", ")}</span>.
            </p>
          )}
          {unavailableServers.length > 0 && (
            <p className="text-muted-foreground mt-1">
              So do the MCP servers that run as a command in the workspace:{" "}
              <span className="font-mono">{unavailableServers.join(", ")}</span>.
            </p>
          )}
        </section>
      )}
    </div>
  );
}

type ToolSwitchProps = {
  tool: Tool;
  /** label is what the row shows: an MCP tool's own name under its server's heading. */
  label: string;
  on: boolean;
  busy: boolean;
  onChange: (name: string, on: boolean) => void;
};

/** ToolSwitch is one tool of the chat, with the switch that offers it to the model or not. */
function ToolSwitch({ tool, label, on, busy, onChange }: ToolSwitchProps) {
  const id = `chat-tool-${tool.name}`;
  return (
    <li className="flex items-start justify-between gap-3 rounded-md border p-2">
      <div className="min-w-0 space-y-0.5">
        <Label htmlFor={id} className="font-mono text-xs break-all">
          {label}
        </Label>
        <p className="text-muted-foreground">{toolSummary(tool.description)}</p>
      </div>
      <Switch
        id={id}
        checked={on}
        disabled={busy}
        aria-label={label === tool.name ? undefined : tool.name}
        onCheckedChange={(next) => {
          onChange(tool.name, next);
        }}
      />
    </li>
  );
}
