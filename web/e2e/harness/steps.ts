/**
 * The step language the `shot` CLI reads: one action per line, so an agent can
 * write a flow without writing TypeScript.
 *
 *   click <target>            fill <target> = <value>     select <target> = <option>
 *   hover <target>            scroll <target>             press <key>
 *   type <text>
 *   send <message>            wait <ms | target>          goto <path>
 *   theme light|dark          viewport <w>x<h>            reload
 *   emit <event json>         shot <name> [= <target>]    fullshot <name>
 *   fail <route> = <failure>  heal <route>
 *   aria                      # comment
 *
 * A target is what the UI calls the thing (see EikaDriver.locate).
 */

import type { EikaEvent } from "../../src/api/events.ts";
import type { EikaDriver } from "./driver.ts";
import { parseFailure } from "./mock.ts";

/** Step is one parsed line. */
export type Step = { verb: string; arg: string; value?: string; line: string };

/** verbs lists every verb with its usage, for errors and `--help`. */
export const verbs: Record<string, string> = {
  click: "click <target>",
  fill: "fill <target> = <value>",
  select: "select <target> = <option>",
  hover: "hover <target>",
  scroll: "scroll <target>      bring it into view, e.g. the end of a long dialog",
  press: "press <key>          e.g. Enter, Escape, Control+K",
  type: "type <text>          into the focused element",
  send: "send <message>       type into the session composer and send",
  wait: "wait <ms | target>",
  goto: "goto <path>",
  reload: "reload",
  theme: "theme light|dark",
  viewport: "viewport <w>x<h>    e.g. 390x844",
  emit: 'emit <event json>    e.g. {"type":"workspace.state","topic":"workspace:ws-retries","payload":{...}}',
  shot: "shot <name> [= <target>]   screenshot the viewport, or one element",
  fullshot: "fullshot <name>      screenshot the whole scrollable page",
  fail: "fail <route> = <failure>   e.g. fail GET /api/providers = 500; also 502 bare, network, hang",
  heal: "heal <route>         let a failed route answer again, as a Retry finds it",
  aria: "aria                 save the accessibility tree",
};

/** parseStep reads one line; it throws with the usage on a malformed one. */
export function parseStep(line: string): Step | null {
  const text = line.trim();
  if (text === "" || text.startsWith("#")) return null;
  const space = text.indexOf(" ");
  const verb = (space < 0 ? text : text.slice(0, space)).toLowerCase();
  const rest = space < 0 ? "" : text.slice(space + 1).trim();
  if (!(verb in verbs)) {
    throw new Error(`unknown step "${text}"; verbs:\n  ${Object.values(verbs).join("\n  ")}`);
  }
  const split = verb === "emit" ? -1 : rest.indexOf(" = ");
  if (split < 0) return { verb, arg: rest, line: text };
  return { verb, arg: rest.slice(0, split).trim(), value: rest.slice(split + 3), line: text };
}

/** StepHooks receive what a step produces. */
export type StepHooks = {
  /** shot is asked for a path to write a named screenshot to. */
  shotPath: (name: string) => string;
  onShot: (file: string) => Promise<void> | void;
  onAria: (yaml: string) => Promise<void> | void;
};

function need(step: Step, what: "arg" | "value"): string {
  const got = what === "arg" ? step.arg : step.value;
  if (got === undefined || got === "") {
    throw new Error(`step "${step.line}" is missing its ${what}; usage: ${verbs[step.verb] ?? ""}`);
  }
  return got;
}

/** runStep performs one step on the driver. */
export async function runStep(driver: EikaDriver, step: Step, hooks: StepHooks): Promise<void> {
  switch (step.verb) {
    case "click":
      return driver.click(need(step, "arg"));
    case "fill":
      return driver.fill(need(step, "arg"), step.value ?? "");
    case "select":
      return driver.select(need(step, "arg"), need(step, "value"));
    case "hover":
      return driver.hover(need(step, "arg"));
    case "scroll":
      return driver.scroll(need(step, "arg"));
    case "press":
      return driver.press(need(step, "arg"));
    case "type":
      return driver.type(need(step, "arg"));
    case "send":
      return driver.send(need(step, "arg"));
    case "wait": {
      const arg = need(step, "arg");
      if (/^\d+$/.test(arg)) {
        await driver.page.waitForTimeout(Number(arg));
        return driver.settle();
      }
      return driver.waitFor(arg);
    }
    case "goto":
      return driver.goto(need(step, "arg"));
    case "reload":
      await driver.page.reload();
      return driver.settle();
    case "theme": {
      const theme = need(step, "arg");
      if (theme !== "light" && theme !== "dark") throw new Error(`usage: ${verbs.theme ?? ""}`);
      return driver.setTheme(theme);
    }
    case "viewport": {
      const match = /^(\d+)x(\d+)$/.exec(need(step, "arg"));
      if (!match) throw new Error(`usage: ${verbs.viewport ?? ""}`);
      return driver.setViewport(Number(match[1]), Number(match[2]));
    }
    case "emit": {
      const e = JSON.parse(need(step, "arg")) as Partial<EikaEvent>;
      if (!e.type || !e.topic) throw new Error("emit needs an event with type and topic");
      driver.mock.emit(driver.mock.event(e.type, e.topic, e.payload));
      return driver.settle();
    }
    case "shot":
    case "fullshot": {
      const file = hooks.shotPath(need(step, "arg"));
      const target = step.value;
      await driver.shot(file, {
        fullPage: step.verb === "fullshot",
        ...(target ? { target } : {}),
      });
      await hooks.onShot(file);
      return;
    }
    case "fail":
      driver.mock.failRoute(need(step, "arg"), parseFailure(need(step, "value")));
      return;
    case "heal":
      driver.mock.healRoute(need(step, "arg"));
      return;
    case "aria":
      await hooks.onAria(await driver.aria());
      return;
  }
}
