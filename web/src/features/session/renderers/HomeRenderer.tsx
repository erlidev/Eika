/** Cards for the home agent's delegation, project and history tools. */

import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";

import { queryKeys } from "@/api/keys";
import { listSessions } from "@/api/routes";
import { OutputBlock } from "@/components/OutputBlock";
import { ResultBlock } from "@/features/session/renderers/parts";
import { stringArg } from "@/features/session/renderers/registry";
import type { ToolRendererProps } from "@/features/session/renderers/registry";

/** SubagentLink opens the named child in Activity when it still exists. */
export function SubagentLink({ name }: { name: string }) {
  const { data } = useQuery({
    queryKey: queryKeys.allSessions(),
    queryFn: ({ signal }) => listSessions(signal),
  });
  const child = data?.find((s) => s.kind === "subagent" && s.name === name);
  if (!child) return null;
  return (
    <Link
      className="text-primary text-xs hover:underline"
      to={`/activity/${encodeURIComponent(child.id)}`}
    >
      View {name} in Activity
    </Link>
  );
}

/** SubagentStatus shows the current child state and opens its session. */
export function SubagentStatus({ name, project }: { name: string; project: string }) {
  const { data } = useQuery({
    queryKey: queryKeys.allSessions(),
    queryFn: ({ signal }) => listSessions(signal),
  });
  const child = data?.find((s) => s.kind === "subagent" && s.name === name);
  if (!child) return null;
  return (
    <div className="border-t px-3 py-1.5 text-xs">
      <Link
        className="text-primary hover:underline"
        to={`/activity/${encodeURIComponent(child.id)}`}
      >
        {child.name} · {child.agent} · {child.state}
        {project !== "" && ` · ${project}`}
      </Link>
      {child.waiting_reason && (
        <p className="text-muted-foreground mt-1 break-words">{child.waiting_reason}</p>
      )}
    </div>
  );
}

/** HomeBody shows the call's named inputs and the full result. */
export function HomeBody({ call }: ToolRendererProps) {
  const argumentsText =
    typeof call.arguments === "string"
      ? call.arguments
      : JSON.stringify(call.arguments ?? null, null, 2);
  const name =
    stringArg(call, "subagent") || (call.name === "subagent_start" ? stringArg(call, "name") : "");
  return (
    <div className="space-y-2">
      <OutputBlock label="arguments" maxHeightClass="max-h-48">
        {argumentsText}
      </OutputBlock>
      {name !== "" && <SubagentLink name={name} />}
      <HomeResult call={call} />
    </div>
  );
}

/** HomeResult formats structured tool results without losing their fields. */
function HomeResult({ call }: ToolRendererProps) {
  if (call.done && !call.isError && call.output === "" && call.content) {
    let formatted = "";
    try {
      const result: unknown = JSON.parse(call.content);
      if (typeof result === "object" && result !== null)
        formatted = JSON.stringify(result, null, 2);
    } catch {
      // Plain text is also a valid tool result.
    }
    if (formatted !== "") return <OutputBlock label="result">{formatted}</OutputBlock>;
  }
  return <ResultBlock call={call} label="result" />;
}
