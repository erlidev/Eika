/**
 * The built-in tool renderers and the registry that maps a tool name to one.
 * Every renderer is a plain component over a `ToolItem`; nothing here reaches
 * the network.
 */

import { OutputBlock } from "@/components/OutputBlock";
import { AskUserBody } from "@/features/session/renderers/AskUserRenderer";
import { EditDiff } from "@/features/session/renderers/EditRenderer";
import { FieldList, ResultBlock } from "@/features/session/renderers/parts";
import { boolArg, detail, numberArg, stringArg } from "@/features/session/renderers/registry";
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

/** editRenderer shows the replacement as a unified diff. */
const editRenderer: ToolRenderer = {
  summary: (call) => {
    const line = detail(call, "line");
    const path = stringArg(call, "path");
    return typeof line === "number" ? `${path}:${String(line)}` : path;
  },
  Body: ({ call }: ToolRendererProps) => (
    <div className="space-y-2">
      <EditDiff
        oldString={stringArg(call, "old_string")}
        newString={stringArg(call, "new_string")}
        startLine={typeof detail(call, "line") === "number" ? (detail(call, "line") as number) : 1}
      />
      {call.isError && <ResultBlock call={call} label="edit error" />}
    </div>
  ),
};

/** writeRenderer shows the path and the content that was written. */
const writeRenderer: ToolRenderer = {
  summary: (call) => stringArg(call, "path"),
  Body: ({ call }: ToolRendererProps) => (
    <div className="space-y-2">
      <FieldList
        fields={[
          ["path", stringArg(call, "path")],
          ["bytes", String(stringArg(call, "content").length)],
        ]}
      />
      <OutputBlock label="written content">{stringArg(call, "content")}</OutputBlock>
    </div>
  ),
};

/** readRenderer shows the range asked for and the file it returned. */
const readRenderer: ToolRenderer = {
  summary: (call) => {
    const offset = numberArg(call, "offset");
    const path = stringArg(call, "path");
    return offset ? `${path} from line ${String(offset)}` : path;
  },
  Body: ({ call }: ToolRendererProps) => (
    <div className="space-y-2">
      <FieldList
        fields={[
          ["path", stringArg(call, "path")],
          ["offset", describeArgument(numberArg(call, "offset"))],
          ["limit", describeArgument(numberArg(call, "limit"))],
        ]}
      />
      <ResultBlock call={call} label="file contents" />
    </div>
  ),
};

/** grepRenderer shows the pattern, where it searched, and the matches. */
const grepRenderer: ToolRenderer = {
  summary: (call) => {
    const path = stringArg(call, "path");
    return path === "" ? stringArg(call, "pattern") : `${stringArg(call, "pattern")} in ${path}`;
  },
  Body: ({ call }: ToolRendererProps) => (
    <div className="space-y-2">
      <FieldList
        fields={[
          ["pattern", stringArg(call, "pattern")],
          ["path", stringArg(call, "path")],
          ["glob", stringArg(call, "glob")],
          ["ignore case", boolArg(call, "ignore_case") ? "yes" : ""],
        ]}
      />
      <ResultBlock call={call} label="matches" />
    </div>
  ),
};

/** findRenderer shows the name pattern and the paths it matched. */
const findRenderer: ToolRenderer = {
  summary: (call) => {
    const path = stringArg(call, "path");
    return path === "" ? stringArg(call, "pattern") : `${stringArg(call, "pattern")} under ${path}`;
  },
  Body: ({ call }: ToolRendererProps) => (
    <div className="space-y-2">
      <FieldList
        fields={[
          ["pattern", stringArg(call, "pattern")],
          ["path", stringArg(call, "path")],
        ]}
      />
      <ResultBlock call={call} label="matched paths" />
    </div>
  ),
};

/** lsRenderer shows the directory it listed. */
const lsRenderer: ToolRenderer = {
  summary: (call) => stringArg(call, "path") || ".",
  Body: ({ call }: ToolRendererProps) => <ResultBlock call={call} label="directory listing" />,
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
 * toolRenderers maps a tool name to its renderer. Register a new tool's
 * renderer here and nowhere else.
 */
export const toolRenderers: Record<string, ToolRenderer> = {
  bash: bashRenderer,
  edit: editRenderer,
  write: writeRenderer,
  read: readRenderer,
  grep: grepRenderer,
  find: findRenderer,
  ls: lsRenderer,
  ask_user: askUserRenderer,
  web_search: webSearchRenderer,
  web_fetch: webFetchRenderer,
};

/** rendererFor returns a tool's renderer, or the JSON fallback. */
export function rendererFor(name: string): ToolRenderer {
  return toolRenderers[name] ?? jsonRenderer;
}
