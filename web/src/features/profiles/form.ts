/**
 * The profile editor's rules as pure functions: the draft a form edits, how
 * it becomes what the harness stores, how a sampling parameter is typed and
 * shown, and how a value that falls through says where it comes from.
 */

import type {
  ConfigLayer,
  Configuration,
  ProfileSettings,
  Sampling,
  SamplingKey,
  Tool,
} from "@/api/types";

/**
 * SamplingField describes one sampling parameter's control: what kind of
 * number or text it takes and the range the harness accepts.
 */
export type SamplingField = {
  key: SamplingKey;
  label: string;
  kind: "decimal" | "integer" | "text" | "list";
  /** hint says what the harness accepts, beside the control. */
  hint: string;
};

/** samplingFields are the sampling parameters in the order the editor shows them. */
export const samplingFields: readonly SamplingField[] = [
  { key: "temperature", label: "Temperature", kind: "decimal", hint: "0 to 2" },
  { key: "top_p", label: "Top p", kind: "decimal", hint: "0 to 1" },
  { key: "top_k", label: "Top k", kind: "integer", hint: "at least 1" },
  { key: "min_p", label: "Min p", kind: "decimal", hint: "0 to 1" },
  { key: "frequency_penalty", label: "Frequency penalty", kind: "decimal", hint: "-2 to 2" },
  { key: "presence_penalty", label: "Presence penalty", kind: "decimal", hint: "-2 to 2" },
  { key: "seed", label: "Seed", kind: "integer", hint: "any whole number" },
  { key: "max_output", label: "Max output tokens", kind: "integer", hint: "at least 1" },
  {
    key: "reasoning_effort",
    label: "Reasoning effort",
    kind: "text",
    hint: "a word the model offers",
  },
  { key: "stop", label: "Stop sequences", kind: "list", hint: "one per line" },
];

/**
 * Draft is what the editor holds: the layer's settings with each sampling
 * parameter as the text of its control, "" for not set, and the tool choice.
 */
export type Draft = {
  model_id: string;
  workspace_prompt: string | null;
  chat_prompt: string | null;
  instructions: string | null;
  context_files: boolean | null;
  sampling: Record<SamplingKey, string>;
  /** tools is the choice, null for none made at this layer. */
  tools: string[] | null;
};

/** samplingText is a sampling parameter as its control shows it; "" is not set. */
export function samplingText(sampling: Sampling, key: SamplingKey): string {
  const value = sampling[key];
  if (value === undefined) return "";
  return Array.isArray(value) ? value.join("\n") : String(value);
}

/** toDraft is a layer's settings and tool choice as the editor edits them. */
export function toDraft(settings: ProfileSettings, tools: string[] | null): Draft {
  const sampling = {} as Record<SamplingKey, string>;
  for (const field of samplingFields)
    sampling[field.key] = samplingText(settings.sampling, field.key);
  return {
    model_id: settings.model_id ?? "",
    workspace_prompt: settings.workspace_prompt,
    chat_prompt: settings.chat_prompt,
    instructions: settings.instructions,
    context_files: settings.context_files,
    sampling,
    tools: tools === null ? null : [...tools],
  };
}

/** SamplingProblem names a parameter whose text is not a value it takes. */
export type SamplingProblem = { key: SamplingKey; message: string };

/**
 * parseSampling reads the sampling controls. A control left empty is not
 * set; one whose text is not a number of its kind is a problem, reported
 * beside it. Ranges are the harness's to check.
 */
export function parseSampling(texts: Record<SamplingKey, string>): {
  sampling: Sampling;
  problems: SamplingProblem[];
} {
  const sampling: Record<string, unknown> = {};
  const problems: SamplingProblem[] = [];
  for (const field of samplingFields) {
    const text = texts[field.key].trim();
    if (text === "") continue;
    switch (field.kind) {
      case "decimal": {
        const n = Number(text);
        if (!Number.isFinite(n)) problems.push({ key: field.key, message: "Enter a number." });
        else sampling[field.key] = n;
        break;
      }
      case "integer": {
        const n = Number(text);
        if (!Number.isSafeInteger(n))
          problems.push({ key: field.key, message: "Enter a whole number." });
        else sampling[field.key] = n;
        break;
      }
      case "text":
        sampling[field.key] = text;
        break;
      case "list":
        sampling[field.key] = texts[field.key].split("\n").filter((line) => line !== "");
        break;
    }
  }
  return { sampling, problems };
}

/**
 * toSettings is what the harness stores for a draft, or the problems that
 * stop it being saved.
 */
export function toSettings(
  draft: Draft,
): { settings: ProfileSettings; problems: [] } | { problems: SamplingProblem[] } {
  const { sampling, problems } = parseSampling(draft.sampling);
  if (problems.length > 0) return { problems };
  return {
    settings: {
      ...(draft.model_id === "" ? {} : { model_id: draft.model_id }),
      workspace_prompt: draft.workspace_prompt,
      chat_prompt: draft.chat_prompt,
      instructions: draft.instructions,
      context_files: draft.context_files,
      sampling,
    },
    problems: [],
  };
}

/** layerNames say where a value falls through to, as the editor words it. */
const layerNames: Record<ConfigLayer, string> = {
  request: "the message",
  session: "this session",
  profile: "the profile",
  model: "the model",
  default: "the default",
};

/** sourceOf is the layer a resolved value came from; a key with none is the default. */
export function sourceOf(config: Configuration, key: string): ConfigLayer {
  return config.sources?.[key] ?? "default";
}

/** layerName is how the editor names a layer: "the profile", "the model". */
export function layerName(layer: ConfigLayer): string {
  return layerNames[layer];
}

/**
 * inheritedSampling is what an unset sampling parameter falls through to,
 * as its control's placeholder says it: the value and where it comes from,
 * or that the endpoint decides.
 */
export function inheritedSampling(config: Configuration, key: SamplingKey): string {
  if (config.sampling[key] === undefined) return "endpoint default";
  const value =
    key === "stop"
      ? (config.sampling.stop ?? []).join(", ") || "none"
      : samplingText(config.sampling, key) || "endpoint default";
  return `${value} (${layerName(sourceOf(config, `sampling.${key}`))})`;
}

/** isSet reports whether a draft sets anything at all. */
export function isSet(draft: Draft): boolean {
  return (
    draft.model_id !== "" ||
    draft.workspace_prompt !== null ||
    draft.chat_prompt !== null ||
    draft.instructions !== null ||
    draft.context_files !== null ||
    draft.tools !== null ||
    Object.values(draft.sampling).some((text) => text.trim() !== "")
  );
}

/** serverEntry is the tool choice entry that takes every tool of an MCP server. */
export function serverEntry(server: string): string {
  return `mcp__${server}__*`;
}

/** ToolGroups are the tools a choice picks from: the built-in ones and each server's. */
export type ToolGroups = {
  builtin: Tool[];
  servers: { server: string; tools: Tool[] }[];
};

/**
 * toolGroups sorts the harness's tools for a choice. A chat's editor leaves
 * out what needs a workspace; a profile's serves both kinds of session.
 */
export function toolGroups(tools: readonly Tool[], chat: boolean): ToolGroups {
  const builtin: Tool[] = [];
  const servers = new Map<string, Tool[]>();
  for (const t of tools) {
    if (chat && t.needs_workspace) continue;
    if (t.server === undefined) builtin.push(t);
    else servers.set(t.server, [...(servers.get(t.server) ?? []), t]);
  }
  return {
    builtin,
    servers: [...servers.keys()]
      .sort()
      .map((server) => ({ server, tools: servers.get(server) ?? [] })),
  };
}

/**
 * chosen reports whether a choice takes a tool: by its name, or through the
 * entry that takes every tool of its server. Null takes every tool.
 */
export function chosen(choice: readonly string[] | null, tool: Tool): boolean {
  if (choice === null) return true;
  return (
    choice.includes(tool.name) ||
    (tool.server !== undefined && choice.includes(serverEntry(tool.server)))
  );
}

/** everyTool is the choice that names every tool of the groups, servers by their entry. */
export function everyTool(groups: ToolGroups): string[] {
  return [
    ...groups.builtin.map((t) => t.name),
    ...groups.servers.map((s) => serverEntry(s.server)),
  ].sort();
}

/**
 * withChoice is a choice after one entry is turned on or off: sorted, each
 * entry once. Turning a server's entry on drops its single tools, which it
 * already takes; turning one of its tools off while the entry is on keeps
 * the server's other tools by name.
 */
export function withChoice(
  choice: readonly string[],
  entry: string,
  on: boolean,
  groups: ToolGroups,
): string[] {
  const next = new Set(choice);
  const group = groups.servers.find(
    (s) => serverEntry(s.server) === entry || s.tools.some((t) => t.name === entry),
  );
  if (group !== undefined && entry === serverEntry(group.server)) {
    for (const t of group.tools) next.delete(t.name);
    if (on) next.add(entry);
    else next.delete(entry);
  } else if (on) {
    next.add(entry);
  } else if (group !== undefined && next.has(serverEntry(group.server))) {
    next.delete(serverEntry(group.server));
    for (const t of group.tools) if (t.name !== entry) next.add(t.name);
  } else {
    next.delete(entry);
  }
  return [...next].sort();
}

/**
 * choiceSummary words a tool choice in a line: every tool, none, or how many
 * tools and servers it names.
 */
export function choiceSummary(choice: readonly string[] | null): string {
  if (choice === null) return "every tool";
  if (choice.length === 0) return "no tools";
  const servers = choice.filter((entry) => entry.startsWith("mcp__") && entry.endsWith("__*"));
  const tools = choice.length - servers.length;
  const parts: string[] = [];
  if (tools > 0) parts.push(`${String(tools)} tool${tools === 1 ? "" : "s"}`);
  if (servers.length > 0) {
    parts.push(`every tool of ${servers.map((s) => s.slice(5, -3)).join(", ")}`);
  }
  return parts.join(" and ");
}

/** PromptChange is how a prompt differs from the built-in one, line by line. */
export type PromptChange = { same: boolean; added: number; removed: number };

/**
 * promptChange compares a prompt with the built-in one it replaces: how many
 * of its lines the built-in text lacks, and how many of the built-in lines
 * it lacks.
 */
export function promptChange(builtin: string, text: string): PromptChange {
  const before = builtin.split("\n");
  const after = text.split("\n");
  const added = after.filter((line) => !before.includes(line)).length;
  const removed = before.filter((line) => !after.includes(line)).length;
  return { same: builtin === text, added, removed };
}
