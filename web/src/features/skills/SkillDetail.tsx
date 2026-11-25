/** The open skill. A placeholder until P6-10 and P6-11: its description and counts. */

import { ArrowLeft } from "lucide-react";
import { Link } from "react-router";

import { ApiError } from "@/api/client";
import { LoadError, Notice } from "@/components/Notice";
import { useSkill } from "@/features/skills/queries";
import { hitLabel, warningLabel } from "@/features/skills/state";

function BackLink() {
  return (
    <Link
      to="/skills"
      className="touch-link text-muted-foreground hover:text-foreground flex w-fit items-center gap-1 text-xs transition-colors lg:hidden"
    >
      <ArrowLeft aria-hidden className="size-3.5" />
      Skills
    </Link>
  );
}

export function SkillDetail({ name }: { name: string }) {
  const skill = useSkill(name);
  return (
    <div className="space-y-2 overflow-y-auto p-3">
      <BackLink />
      {skill.isPending ? (
        <Notice tone="pending">Loading the skill…</Notice>
      ) : skill.isError ? (
        skill.error instanceof ApiError && skill.error.status === 404 ? (
          <Notice>This skill no longer exists.</Notice>
        ) : (
          <LoadError
            what="the skill"
            error={skill.error}
            retry={() => void skill.refetch()}
            retrying={skill.isFetching}
          />
        )
      ) : (
        <>
          <h2 className="text-base font-semibold">{skill.data.name}</h2>
          <p className="text-sm">{skill.data.description}</p>
          <p className="text-muted-foreground text-xs">
            {skill.data.enabled ? "Enabled" : "Disabled"} · {String(skill.data.files.length)} files
            {[
              warningLabel({ warning_count: skill.data.warnings.length }),
              hitLabel(skill.data.screen?.spans.length ?? 0),
            ]
              .filter((s) => s !== "")
              .map((s) => ` · ${s}`)
              .join("")}
          </p>
        </>
      )}
    </div>
  );
}
