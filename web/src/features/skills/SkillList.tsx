/** The list: the install area, then a row per skill with its state and warning count. */

import { NavLink } from "react-router";

import type { SkillItem } from "@/api/types";
import { LoadError, Notice } from "@/components/Notice";
import { Installer } from "@/features/skills/Installer";
import { useSkills } from "@/features/skills/queries";
import { hitLabel, warningLabel } from "@/features/skills/state";
import { cn } from "@/lib/utils";

export function SkillList() {
  const skills = useSkills();
  return (
    <>
      <Installer />
      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto">
        {skills.isPending ? (
          <div className="p-3">
            <Notice tone="pending">Loading the skills…</Notice>
          </div>
        ) : skills.isError ? (
          <div className="p-3">
            <LoadError
              what="the skills"
              error={skills.error}
              retry={() => void skills.refetch()}
              retrying={skills.isFetching}
            />
          </div>
        ) : skills.data.length === 0 ? (
          <p className="text-muted-foreground p-4 text-sm">
            No skills yet. Install one from a zip above.
          </p>
        ) : (
          <ul>
            {skills.data.map((s) => (
              <li key={s.name}>
                <Row skill={s} />
              </li>
            ))}
          </ul>
        )}
      </div>
    </>
  );
}

const chip = "shrink-0 rounded-full border px-2 py-0.5 text-xs";

export function StateChips({ skill }: { skill: SkillItem }) {
  const warnings = warningLabel(skill);
  const hits = hitLabel(skill.span_count);
  return (
    <>
      {skill.broken !== "" && (
        <span className={cn(chip, "border-destructive/40 text-destructive")}>Broken</span>
      )}
      {warnings !== "" && (
        <span className={cn(chip, "border-warning/40 text-warning")}>{warnings}</span>
      )}
      {hits !== "" && <span className={cn(chip, "border-warning/40 text-warning")}>{hits}</span>}
      <span
        className={cn(
          chip,
          skill.enabled ? "border-success/40 text-success" : "text-muted-foreground",
        )}
      >
        {skill.enabled ? "Enabled" : "Disabled"}
      </span>
    </>
  );
}

function Row({ skill }: { skill: SkillItem }) {
  return (
    <NavLink
      to={`/skills/${encodeURIComponent(skill.name)}`}
      className="hover:bg-accent aria-[current=page]:bg-accent focus-visible:ring-ring flex min-h-11 min-w-0 flex-col justify-center gap-0.5 px-3 py-1.5 transition-colors focus-visible:ring-1 focus-visible:outline-none"
    >
      <span className="min-w-0 truncate text-sm font-medium">{skill.name}</span>
      <span className="flex min-w-0 flex-wrap items-center gap-1">
        <StateChips skill={skill} />
      </span>
      <span className="text-muted-foreground min-w-0 truncate text-xs">{skill.description}</span>
    </NavLink>
  );
}
