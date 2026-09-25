/**
 * The built-in tool renderers and the registry that maps a tool name to one.
 * Every renderer is a plain component over a `ToolItem`; nothing here reaches
 * the network.
 */

import { OutputBlock } from "@/components/OutputBlock";
import { AskUserBody } from "@/features/session/renderers/AskUserRenderer";
import { MCPToolBody } from "@/features/session/renderers/MCPRenderer";
import { ResultBlock } from "@/features/session/renderers/parts";
import { detail, stringArg } from "@/features/session/renderers/registry";
import type { ToolRenderer, ToolRendererProps } from "@/features/session/renderers/registry";
import { WebFetchBody, WebSearchBody } from "@/features/session/renderers/WebRenderer";
import { describeArgument, firstLine } from "@/lib/format";

/** bashRenderer shows the command and its streamed output with an exit code. */
const bashRenderer: ToolRenderer = {
  summary: (call) => firstLine(stringArg(call, "command")),
  Body: ({ call }: ToolRendererProps) => {
    const exit = detail(call, "exit_code");
    const timedOut = detail(call, "timed_out") === true;
    return (
      <div className="space-y-2">
        <OutputBlock label="command" maxHeightClass="max-h-24" className="bg-muted">
          {`$ ${stringArg(call, "command")}`}
        </OutputBlock>
        <ResultBlock call={call} label="command output" />
        {call.done && timedOut && (
          <p className="text-muted-foreground font-mono text-xs">timed out and was killed</p>
        )}
        {call.done && !timedOut && typeof exit === "number" && (
          <p className="text-muted-foreground font-mono text-xs">exit code {exit}</p>
        )}
      </div>
    );
  },
};

/** askUserRenderer renders the question form the run is blocked on. */
const askUserRenderer: ToolRenderer = {
  summary: (call) => firstLine(stringArg(call, "question")),
  Body: AskUserBody,
};

/** webSearchRenderer shows the query and the results the model received. */
const webSearchRenderer: ToolRenderer = {
  summary: (call) => {
    const source = stringArg(call, "source");
    const query = stringArg(call, "query");
    return source === "" || source === "web" ? query : `${query} in ${source}`;
  },
  Body: WebSearchBody,
};

/** webFetchRenderer shows the page asked for, how it was narrowed, and its content. */
const webFetchRenderer: ToolRenderer = {
  summary: (call) => {
    const url = stringArg(call, "url");
    const section = stringArg(call, "section");
    if (section !== "") return `${url} § ${section}`;
    return stringArg(call, "filter") === "" ? url : `${url} (filtered)`;
  },
  Body: WebFetchBody,
};

/** jsonRenderer is the fallback: the arguments and the result, as they are. */
const jsonRenderer: ToolRenderer = {
  summary: (call) => firstLine(describeArgument(call.arguments), 80),
  Body: ({ call }: ToolRendererProps) => (
    <div className="space-y-2">
      <OutputBlock label="arguments" maxHeightClass="max-h-48">
        {JSON.stringify(call.arguments ?? {}, null, 2)}
      </OutputBlock>
      <ResultBlock call={call} label="result" />
    </div>
  ),
};

/**
 * mcpRenderer draws every tool an MCP server offers, and the resource tools:
 * the arguments, a request of the server for input while one waits, and the
 * content blocks it sent.
 */
const mcpRenderer: ToolRenderer = {
  summary: (call) => {
    const uri = stringArg(call, "uri");
    return uri !== "" ? uri : firstLine(describeArgument(call.arguments), 80);
  },
  Body: MCPToolBody,
};

/**
 * toolRenderers maps a tool name to its renderer. Register a new tool's
 * renderer here and nowhere else.
 */
export const toolRenderers: Record<string, ToolRenderer> = {
  bash: bashRenderer,
  ask_user: askUserRenderer,
  web_search: webSearchRenderer,
  web_fetch: webFetchRenderer,
};

/**
 * rendererFor returns a tool's renderer: its own, the MCP renderer for a
 * name the MCP pool gives its tools (`mcp_` is reserved for it), or the JSON
 * fallback.
 */
export function rendererFor(name: string): ToolRenderer {
  return toolRenderers[name] ?? (name.startsWith("mcp_") ? mcpRenderer : jsonRenderer);
}
