/**
 * A scripted OpenAI-compatible endpoint for the compose smoke test. It runs
 * as the `model` service of compose.smoke.yaml, with no dependency beyond
 * Node, and answers the way a model that uses its tools would:
 *
 * - `GET /v1/models` lists one model, `smoke`.
 * - A chat request whose tools include `bash` and whose last message is the
 *   user's asks for `echo smoke-ok`.
 * - A chat request that ends on a tool result says what the command printed.
 * - Anything else, such as the provider test's request, answers "ok".
 *
 * Every chat answer is streamed as Chat Completions chunks with usage.
 */

import { createServer } from "node:http";
import type { ServerResponse } from "node:http";

type Message = { role: string; content?: unknown };
type ChatRequest = { messages?: Message[]; tools?: { function?: { name?: string } }[] };

const port = Number(process.env.PORT ?? "8000");

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

function answer(req: ChatRequest): object[] {
  const last = req.messages?.at(-1);
  const hasBash = req.tools?.some((t) => t.function?.name === "bash") ?? false;
  if (last?.role === "tool") {
    const output = typeof last.content === "string" ? last.content.trim() : "";
    return [
      chunk({ role: "assistant", content: `The command printed ${output}.` }),
      chunk({}, "stop"),
    ];
  }
  if (hasBash && last?.role === "user") {
    const call = {
      index: 0,
      id: "call_smoke",
      type: "function",
      function: { name: "bash", arguments: JSON.stringify({ command: "echo smoke-ok" }) },
    };
    return [
      chunk({ role: "assistant", content: "Running it." }),
      chunk({ tool_calls: [call] }),
      chunk({}, "tool_calls"),
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
