/**
 * The API's wire contract, docs/api/contract.json, which the Go server's tests
 * write from its wire types and check its handlers against. The mock harness
 * holds itself to the same file: it refuses a request the harness would
 * refuse, and reports a response or event the harness could not send, so the
 * UI is never tested against an API that does not exist.
 *
 * This mirrors internal/server/servertest; a change to what a shape can say
 * changes both.
 */

import { readFileSync } from "node:fs";

/**
 * Shape is the JSON form of a value: a primitive by name, or an object keyed
 * by its kind. See internal/server/servertest.
 */
export type Shape =
  | "string"
  | "number"
  | "boolean"
  | "any"
  | { array: Shape }
  | { map: Shape }
  | { nullable: Shape }
  | { ref: string }
  | { object: Record<string, Shape>; optional?: string[] };

/** Route is what one route reads and answers. */
export type Route = {
  status?: number;
  request?: Shape;
  raw_request?: boolean;
  response?: Shape;
  websocket?: boolean;
  public?: boolean;
};

export type Contract = {
  routes: Record<string, Route>;
  error: Shape;
  error_codes: string[];
  events: Record<string, Shape | null>;
  types?: Record<string, Shape>;
};

let loaded: Contract | undefined;

/** contract reads docs/api/contract.json once. */
export function contract(): Contract {
  loaded ??= JSON.parse(
    readFileSync(new URL("../../../docs/api/contract.json", import.meta.url), "utf8"),
  ) as Contract;
  return loaded;
}

/**
 * matchRoute finds the route that serves a method and path, as routes.go
 * does: a literal segment wins over a `{parameter}`.
 */
export function matchRoute(
  c: Contract,
  method: string,
  path: string,
): { key: string; route: Route } | undefined {
  let best: { key: string; route: Route; literals: number } | undefined;
  const got = path.split("/");
  for (const [key, route] of Object.entries(c.routes)) {
    const [keyMethod, pattern = ""] = key.split(" ");
    if (keyMethod !== method) continue;
    const want = pattern.split("/");
    if (want.length !== got.length) continue;
    let literals = 0;
    const fits = want.every((segment, i) => {
      if (segment.startsWith("{")) return got[i] !== "";
      literals += 1;
      return segment === got[i];
    });
    if (fits && (best === undefined || literals > best.literals)) best = { key, route, literals };
  }
  return best && { key: best.key, route: best.route };
}

/**
 * check reports every way value differs from shape, one line per problem,
 * each starting with its path: `$.projects[0].name: number, want string`. In
 * a request a field may be missing or null, as the harness's decoder allows,
 * but an unknown field is still a problem, as the decoder refuses it.
 */
export function check(
  shape: Shape,
  value: unknown,
  options: { request?: boolean; types?: Record<string, Shape> } = {},
): string[] {
  const problems: string[] = [];
  const request = options.request === true;
  const types = options.types ?? {};

  const walk = (s: Shape, v: unknown, path: string): void => {
    if (typeof s === "object" && "ref" in s) {
      const def = types[s.ref];
      if (def === undefined) {
        problems.push(`${path}: no shape for ${s.ref}`);
        return;
      }
      s = def;
    }
    if (v === null || v === undefined) {
      if (s !== "any" && !(typeof s === "object" && "nullable" in s) && !request) {
        problems.push(`${path}: null, want ${describe(s)}`);
      }
      return;
    }
    if (s === "any") return;
    if (typeof s === "string") {
      if (kindOf(v) !== s) problems.push(`${path}: ${kindOf(v)}, want ${s}`);
      return;
    }
    if ("nullable" in s) {
      walk(s.nullable, v, path);
    } else if ("array" in s) {
      if (!Array.isArray(v)) {
        problems.push(`${path}: ${kindOf(v)}, want array`);
        return;
      }
      v.forEach((item, i) => {
        walk(s.array, item, `${path}[${String(i)}]`);
      });
    } else if ("map" in s) {
      if (kindOf(v) !== "object") {
        problems.push(`${path}: ${kindOf(v)}, want map`);
        return;
      }
      for (const [k, item] of sortedEntries(v as Record<string, unknown>)) {
        walk(s.map, item, `${path}.${k}`);
      }
    } else if ("object" in s) {
      if (kindOf(v) !== "object") {
        problems.push(`${path}: ${kindOf(v)}, want object`);
        return;
      }
      const record = v as Record<string, unknown>;
      for (const [k, item] of sortedEntries(record)) {
        const field = s.object[k];
        if (field === undefined) {
          problems.push(`${path}.${k}: unknown field`);
          continue;
        }
        walk(field, item, `${path}.${k}`);
      }
      if (request) return;
      for (const k of Object.keys(s.object).sort()) {
        if (!(k in record) && !(s.optional ?? []).includes(k)) {
          problems.push(`${path}.${k}: missing`);
        }
      }
    }
  };

  // What is checked is what goes over the wire: JSON leaves out a field set
  // to undefined, and a Date or a class instance becomes what it encodes to.
  walk(shape, value === undefined ? undefined : JSON.parse(JSON.stringify(value)), "$");
  return problems;
}

function describe(s: Shape): string {
  if (typeof s === "string") return s;
  if ("nullable" in s) return `${describe(s.nullable)} or null`;
  if ("ref" in s) return "object";
  return Object.keys(s)[0] ?? "any";
}

function kindOf(v: unknown): string {
  if (Array.isArray(v)) return "array";
  if (v === null) return "null";
  return typeof v;
}

function sortedEntries(record: Record<string, unknown>): [string, unknown][] {
  return Object.entries(record).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
}
