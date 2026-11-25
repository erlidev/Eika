/** The pure part of the Skills page: how an install and a list row are worded. */

import type { Skill, SkillInstall, SkillItem } from "@/api/types";

const plural = (n: number, one: string, many: string) => `${String(n)} ${n === 1 ? one : many}`;

/** warningLabel is a row's warning count, or "" when there is nothing to say. */
export function warningLabel(skill: Pick<SkillItem, "warning_count">): string {
  return skill.warning_count === 0 ? "" : plural(skill.warning_count, "warning", "warnings");
}

/** hitLabel is how many spans the install screen marked, or "" for none. */
export function hitLabel(spans: number): string {
  return spans === 0 ? "" : plural(spans, "screen hit", "screen hits");
}

/** InstallSummary is what an install answered, ready to show. */
export type InstallSummary = {
  /** tone is warning when there is something to read before enabling. */
  tone: "success" | "warning" | "info";
  text: string;
  details: string[];
};

/** summarize words an install: unchanged, or new and disabled with its warnings and hits. */
export function summarize(result: SkillInstall): InstallSummary {
  const { skill } = result;
  if (result.unchanged) {
    return {
      tone: "info",
      text: `${skill.name} is already installed and unchanged.`,
      details: [],
    };
  }
  const spans = skill.screen?.spans.length ?? 0;
  const details = [
    ...skill.warnings,
    ...(skill.screen?.unscanned === true
      ? ["The injection screen did not run, so this skill was not checked."]
      : []),
  ];
  const counts = [warningLabel({ warning_count: skill.warnings.length }), hitLabel(spans)].filter(
    (s) => s !== "",
  );
  const flagged = counts.length > 0 || details.length > 0;
  return {
    tone: flagged ? "warning" : "success",
    text:
      `Installed ${skill.name}. It is disabled until you enable it` +
      (counts.length > 0 ? `; ${counts.join(" and ")}.` : "."),
    details,
  };
}

/** FieldRow is one of the six SKILL.md fields, by its spec name, worded for display. */
export type FieldRow = { label: string; value: string };

/** fieldRows lists the six fields in the spec's order; an unset one has an empty value. */
export function fieldRows(skill: Skill): FieldRow[] {
  const metadata = Object.entries(skill.metadata)
    .map(([k, v]) => `${k}: ${v}`)
    .join("\n");
  return [
    { label: "name", value: skill.name },
    { label: "description", value: skill.description },
    { label: "license", value: skill.license },
    { label: "compatibility", value: skill.compatibility },
    { label: "metadata", value: metadata },
    { label: "allowed-tools", value: skill.allowed_tools },
  ];
}
