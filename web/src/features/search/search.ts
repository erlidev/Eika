/**
 * The pure rules behind the search settings: which providers are in the
 * failover order, what quota each bucket has, and how usage reads.
 */

import type { SearchBackendStatus, SearchLimit, SettingsState } from "@/api/types";

/** searchSettingKeys are the settings keys search reads. */
export const searchSettingKeys = {
  order: "search_order",
  limits: "search_limits",
} as const;

/**
 * readOrder returns the web provider order in force: the stored one when it
 * is a list of known providers, the default otherwise.
 */
export function readOrder(state: SettingsState): string[] {
  const stored = state.settings[searchSettingKeys.order];
  const known = state.defaults.search_order;
  if (!Array.isArray(stored)) return [...known];
  return stored.filter((name): name is string => typeof name === "string" && known.includes(name));
}

/** readLimits returns every bucket's quota: the defaults, overridden by what is stored. */
export function readLimits(state: SettingsState): Record<string, SearchLimit> {
  const limits: Record<string, SearchLimit> = { ...state.defaults.search_limits };
  const stored = state.settings[searchSettingKeys.limits];
  if (typeof stored !== "object" || stored === null || Array.isArray(stored)) return limits;
  for (const [bucket, value] of Object.entries(stored)) {
    if (!(bucket in limits) || typeof value !== "object" || value === null) continue;
    const { day, month } = value as Record<string, unknown>;
    limits[bucket] = {
      ...(typeof day === "number" && day > 0 ? { day } : {}),
      ...(typeof month === "number" && month > 0 ? { month } : {}),
    };
  }
  return limits;
}

/** moveProvider moves a provider one place up (-1) or down (+1), staying in bounds. */
export function moveProvider(order: readonly string[], name: string, delta: -1 | 1): string[] {
  const from = order.indexOf(name);
  const to = from + delta;
  if (from < 0 || to < 0 || to >= order.length) return [...order];
  const next = [...order];
  next.splice(from, 1);
  next.splice(to, 0, name);
  return next;
}

/**
 * toggleProvider takes a provider out of the order, or puts it back at the
 * end. A provider left out is never queried.
 */
export function toggleProvider(order: readonly string[], name: string): string[] {
  return order.includes(name) ? order.filter((n) => n !== name) : [...order, name];
}

/** usageText renders a bucket's use against its quota, the way the status report does. */
export function usageText(backend: Pick<SearchBackendStatus, "usage" | "limit">): string {
  const { usage, limit } = backend;
  if (limit.month) return `${String(usage.month_used)}/${String(limit.month)} this month`;
  if (limit.day) return `${String(usage.day_used)}/${String(limit.day)} today`;
  return `${String(usage.day_used)} today, unlimited`;
}

/** maxLimit is the largest quota the harness accepts; a bigger number is a typo. */
export const maxLimit = 10_000_000;

/**
 * LimitField is one quota field as the user left it. badInput is the
 * browser's report that a number field holds text it cannot read as a
 * number, which it shows but reports as an empty value.
 */
export type LimitField = { text: string; badInput?: boolean };

/**
 * limitProblem says what is wrong with a quota field, or null when it is
 * empty (unlimited) or a whole number from 1 to maxLimit. Nothing is ever
 * read as unlimited except an empty field.
 */
export function limitProblem(field: LimitField): string | null {
  if (field.badInput) return "Enter a whole number, or clear the field for unlimited.";
  const text = field.text.trim();
  if (text === "") return null;
  if (!/^-?\d+$/.test(text)) return "Enter a whole number, or clear the field for unlimited.";
  const n = Number(text);
  if (n < 1) return "A quota must be at least 1. Clear the field for unlimited.";
  if (n > maxLimit) return `A quota may be at most ${maxLimit.toLocaleString("en-US")}.`;
  return null;
}

/**
 * parseLimit reads a quota field that limitProblem accepted: a whole number,
 * or undefined for an empty field, which means unlimited.
 */
export function parseLimit(text: string): number | undefined {
  const trimmed = text.trim();
  return trimmed === "" ? undefined : Number(trimmed);
}

/** keyLabels names the search keys for people. */
export const keyLabels: Record<string, string> = {
  exa: "Exa",
  tavily: "Tavily",
  brave: "Brave Search",
  github: "GitHub token",
};

/**
 * safeHref returns a URL a link may point at: an absolute http or https URL.
 * Results come from the web, so anything else, a javascript: URL above all,
 * is shown as text instead.
 */
export function safeHref(url: string): string | undefined {
  try {
    const parsed = new URL(url);
    return parsed.protocol === "http:" || parsed.protocol === "https:" ? parsed.href : undefined;
  } catch {
    return undefined;
  }
}
