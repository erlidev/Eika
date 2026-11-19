/**
 * A scripted OpenAI-compatible endpoint for the compose smoke test. It runs
 * as the `model` service of compose.smoke.yaml, with no dependency beyond
 * Node, and answers the way a model that uses its tools would:
 *
 * - `GET /v1/models` lists one model, `smoke`.
 * - Eika delegates the probe to a worker with `subagent_start`.
 * - The worker calls `bash` and reports the command's output.
 * - Anything else, such as the provider test's request, answers "ok".
 *
 * Every chat answer is streamed as Chat Completions chunks with usage.
 */

import { createServer } from "node:http";
import type { ServerResponse } from "node:http";

type Message = { role: string; content?: unknown };
type ChatRequest = { messages?: Message[]; tools?: { function?: { name?: string } }[] };

const port = Number(process.env.PORT ?? "8000");

/**
 * sandboxProbe is the command the model asks `bash` to run. It prints the
 * hostname, which Docker sets to the container's short id, and the working
 * directory, so the answer proves which container ran it: the spec compares
 * the hostname with the home computer's container.
 */
const sandboxProbe = "echo smoke-ok $(hostname) $(pwd)";

/** chunk is one streamed Chat Completions chunk with one choice. */
function chunk(delta: object, finish: string | null = null): object {
  return {
    id: "smoke",
    object: "chat.completion.chunk",
    created: 0,
    model: "smoke",
    choices: [{ index: 0, delta, finish_reason: finish }],
  };
}

function stream(res: ServerResponse, chunks: object[]): void {
  res.writeHead(200, { "Content-Type": "text/event-stream" });
  const usage = { prompt_tokens: 100, completion_tokens: 10, total_tokens: 110 };
  for (const c of [
    ...chunks,
    {
      id: "smoke",
      object: "chat.completion.chunk",
      created: 0,
      model: "smoke",
      choices: [],
      usage,
    },
  ]) {
    res.write(`data: ${JSON.stringify(c)}\n\n`);
  }
  res.end("data: [DONE]\n\n");
}

function toolCall(name: string, args: object): object[] {
  return [
    chunk({ role: "assistant" }),
    chunk({
      tool_calls: [
        {
          index: 0,
          id: `call_${name}`,
          type: "function",
          function: { name, arguments: JSON.stringify(args) },
        },
      ],
    }),
    chunk({}, "tool_calls"),
  ];
}

function answer(req: ChatRequest): object[] {
  const last = req.messages?.at(-1);
  const content = typeof last?.content === "string" ? last.content : "";
  const hasTool = (name: string) => req.tools?.some((t) => t.function?.name === name) ?? false;
  if (last?.role === "user" && hasTool("subagent_start") && content.includes("Run the probe")) {
    return toolCall("subagent_start", {
      agent: "worker",
      name: "smoke-worker",
      brief: "Run the sandbox probe.",
    });
  }
  if (last?.role === "user" && hasTool("bash")) {
    if (content.includes("Draft your report now")) {
      return toolCall("report", { summary: "Sandbox probe complete." });
    }
    return toolCall("bash", { command: sandboxProbe });
  }
  if (last?.role === "tool" && hasTool("bash") && content.includes("smoke-ok")) {
    return [
      chunk({ role: "assistant", content: `The command printed ${content.trim()}.` }),
      chunk({}, "stop"),
    ];
  }
  return [chunk({ role: "assistant", content: "ok" }), chunk({}, "stop")];
}

createServer((req, res) => {
  const url = req.url ?? "";
  if (req.method === "GET" && url.endsWith("/models")) {
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(
      JSON.stringify({
        object: "list",
        data: [{ id: "smoke", object: "model", owned_by: "eika" }],
      }),
    );
    return;
  }
  if (req.method === "POST" && url.endsWith("/chat/completions")) {
    let body = "";
    req.on("data", (d: Buffer) => (body += d.toString()));
    req.on("end", () => {
      stream(res, answer(JSON.parse(body) as ChatRequest));
    });
    return;
  }
  res.writeHead(404).end();
}).listen(port, () => {
  console.log(`smoke model listening on :${String(port)}`);
});
