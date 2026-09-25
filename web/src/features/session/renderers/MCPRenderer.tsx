/**
 * The card of an MCP server's tool, `mcp__<server>__<tool>`, and of the
 * resource tools. While the server asks the user for input, the card is that
 * request; once the call has finished it shows what the server sent, images
 * and audio included, which the model only reads a line about.
 */

import type { MCPToolDetails } from "@/api/types";
import { OutputBlock } from "@/components/OutputBlock";
import { ContentBlocks } from "@/features/mcp";
import { ElicitationForm } from "@/features/session/renderers/ElicitationForm";
import { FieldList, ResultBlock } from "@/features/session/renderers/parts";
import { serverTool } from "@/features/session/renderers/registry";
import type { ToolRendererProps } from "@/features/session/renderers/registry";
import { useRunStatus } from "@/features/session/queries";
import { useSessionStore } from "@/features/session/store";

/** mcpDetails narrows a call's details to an MCP result's, undefined before it finishes. */
function mcpDetails(details: unknown): MCPToolDetails | undefined {
  return typeof details === "object" && details !== null && "content" in details
    ? (details as MCPToolDetails)
    : undefined;
}

/** MCPToolBody is the renderer body registered for every MCP tool. */
export function MCPToolBody({ call }: ToolRendererProps) {
  const sessionId = useSessionStore((s) => s.sessionId);
  const live = useSessionStore((s) => s.elicitations.find((x) => x.call_id === call.callId));
  // A request made before this page subscribed is in the run state only.
  const status = useRunStatus(sessionId);
  const asked = call.done
    ? undefined
    : (live ?? status.data?.elicitations.find((x) => x.call_id === call.callId));
  const d = mcpDetails(call.details);
  const named = serverTool(call.name);

  return (
    <div className="space-y-2">
      <FieldList
        fields={[
          ["server", d?.server ?? named?.server ?? ""],
          ["tool", d?.tool ?? named?.tool ?? ""],
        ]}
      />
      <OutputBlock label="arguments" maxHeightClass="max-h-48">
        {JSON.stringify(call.arguments ?? {}, null, 2)}
      </OutputBlock>
      {asked !== undefined && <ElicitationForm key={asked.id} elicitation={asked} />}
      {d !== undefined ? (
        <>
          <ContentBlocks
            blocks={d.content ?? []}
            tone={call.isError || d.is_error === true ? "error" : "default"}
          />
          {d.structured_content !== undefined && (
            <OutputBlock label="structured result" maxHeightClass="max-h-48">
              {JSON.stringify(d.structured_content, null, 2)}
            </OutputBlock>
          )}
        </>
      ) : (
        asked === undefined && <ResultBlock call={call} label="result" />
      )}
    </div>
  );
}
