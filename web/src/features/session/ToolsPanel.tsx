/**
 * The tools panel of a chat: which tools the model may use, chosen with the
 * same picker the configuration editor uses, and the tools a chat never has.
 * It is how a chat reads as a chat beside the transcript: where a workspace
 * session has its files, terminal, and changes, a chat has this. A change is
 * the session's own tool choice; Reset goes back to its profile's.
 */

import { ActionError, LoadError, Notice } from "@/components/Notice";
import { FieldHeader, ResetButton } from "@/features/profiles/fields";
import { everyTool, sourceOf, toolGroups } from "@/features/profiles/form";
import { useSessionConfiguration } from "@/features/profiles/queries";
import { ToolPicker } from "@/features/profiles/ToolPicker";
import { useSetSessionTools, useTools } from "@/features/session/queries";
import { outOfChat } from "@/features/session/tools";

export type ToolsPanelProps = {
  sessionId: string;
};

export function ToolsPanel({ sessionId }: ToolsPanelProps) {
  const config = useSessionConfiguration(sessionId);
  const tools = useTools();
  const setTools = useSetSessionTools(sessionId);

  if (config.isPending || tools.isPending) {
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
  if (config.isError) {
    return (
      <div className="p-3">
        <LoadError
          what="the chat's configuration"
          error={config.error}
          retrying={config.isFetching}
          retry={() => void config.refetch()}
        />
      </div>
    );
  }

  const groups = toolGroups(tools.data, true);
  const inherited = config.data.inherited.tools ?? everyTool(groups);
  // While a change is on its way the switches show it, so a click is not
  // undone on screen by the value the harness has not answered yet.
  const pending = setTools.isPending ? setTools.variables : undefined;
  const own = pending === undefined ? config.data.tools : pending;
  const choice = own ?? inherited;
  const out = outOfChat(tools.data);

  return (
    <div className="space-y-4 p-3 text-xs">
      <p className="text-muted-foreground">
        A chat has no workspace, so the model can use only the tools below. A change applies from
        the next message.
      </p>
      <div className="space-y-2">
        <FieldHeader
          label="Tools in this chat"
          set={own !== null}
          from={sourceOf(config.data.inherited, "tools")}
        >
          {own !== null && (
            <ResetButton
              label="the chat's tools to its profile's"
              onClick={() => {
                setTools.mutate(null);
              }}
            />
          )}
        </FieldHeader>
        <ToolPicker
          idPrefix="chat-tool"
          groups={groups}
          choice={choice}
          busy={setTools.isPending}
          onChange={(next) => {
            setTools.mutate(next);
          }}
        />
        {choice.length === 0 && (
          <p className="text-muted-foreground">
            Every tool is off: the model answers from what it knows.
          </p>
        )}
        {setTools.isError && <ActionError action="change the tools" error={setTools.error} />}
      </div>

      {(out.tools.length > 0 || out.servers.length > 0) && (
        <section>
          <h3 className="text-muted-foreground mb-1 text-2xs font-semibold tracking-wide uppercase">
            Need a workspace
          </h3>
          {out.tools.length > 0 && (
            <p className="text-muted-foreground">
              Files, commands, and child agents belong to a workspace session:{" "}
              <span className="font-mono">{out.tools.join(", ")}</span>.
            </p>
          )}
          {out.servers.length > 0 && (
            <p className="text-muted-foreground mt-1">
              So do the MCP servers that run as a command in the workspace:{" "}
              <span className="font-mono">{out.servers.join(", ")}</span>.
            </p>
          )}
        </section>
      )}
    </div>
  );
}
