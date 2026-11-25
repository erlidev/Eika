/** The open skill: what the user reads before enabling it, with the screen's hits marked. */

import { ArrowLeft } from "lucide-react";
import { Link } from "react-router";

import { ApiError } from "@/api/client";
import type { Skill } from "@/api/types";
import { LoadError, Notice } from "@/components/Notice";
import { useSkill } from "@/features/skills/queries";
import { fieldRows } from "@/features/skills/state";
import { formatAgo, formatBytes } from "@/lib/format";
import { segments } from "@/lib/segments";

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
  if (skill.isPending) {
    return (
      <div className="space-y-2 p-3">
        <BackLink />
        <Notice tone="pending">Loading the skill…</Notice>
      </div>
    );
  }
  if (skill.isError) {
    const gone = skill.error instanceof ApiError && skill.error.status === 404;
    return (
      <div className="space-y-2 p-3">
        <BackLink />
        {gone ? (
          <Notice>This skill no longer exists.</Notice>
        ) : (
          <LoadError
            what="the skill"
            error={skill.error}
            retry={() => void skill.refetch()}
            retrying={skill.isFetching}
          />
        )}
      </div>
    );
  }
  return <Body skill={skill.data} />;
}

/** Marked shows a field's text with the screened spans marked. */
function Marked({ skill, field }: { skill: Skill; field: "description" | "body" }) {
  return (
    <>
      {segments(skill[field], field, skill.screen?.spans ?? []).map((part, i) =>
        part.flagged ? (
          <mark key={i} className="bg-warning/20 text-foreground rounded-md px-0.5">
            {part.text}
          </mark>
        ) : (
          <span key={i}>{part.text}</span>
        ),
      )}
    </>
  );
}

function Body({ skill }: { skill: Skill }) {
  const spans = skill.screen?.spans ?? [];
  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <article className="mx-auto max-w-3xl space-y-3 p-4">
        <BackLink />
        <header className="space-y-1">
          <h2 className="text-lg font-semibold break-words">{skill.name}</h2>
          <p className="text-muted-foreground text-xs">
            {skill.enabled ? "Enabled" : "Disabled"} · installed {formatAgo(skill.installed_at)}
          </p>
        </header>
        {skill.broken !== "" && <Notice tone="error">Broken: {skill.broken}</Notice>}
        {skill.screen?.unscanned === true && (
          <Notice tone="warning">
            The injection screen did not run, so this skill was not checked. Read it all before
            enabling it.
          </Notice>
        )}
        {spans.length > 0 && (
          <Notice tone="warning">
            The screen flagged{" "}
            {spans.length === 1 ? "1 passage" : `${String(spans.length)} passages`}, marked below.
            <ul className="mt-1 space-y-0.5">
              {spans.map((s, i) => (
                <li key={`${s.field}-${String(s.start)}-${String(i)}`}>
                  Flagged in the {s.field}: <q className="font-mono break-words">{s.text}</q>
                </li>
              ))}
            </ul>
          </Notice>
        )}
        {skill.warnings.length > 0 && (
          <Notice tone="warning">
            <ul className="space-y-0.5">
              {skill.warnings.map((w, i) => (
                <li key={i}>{w}</li>
              ))}
            </ul>
          </Notice>
        )}
        <section aria-label="Fields">
          <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm">
            {fieldRows(skill).map((row) => (
              <div key={row.label} className="contents">
                <dt className="text-muted-foreground text-xs">{row.label}</dt>
                <dd className="break-words">
                  {row.label === "description" ? (
                    <Marked skill={skill} field="description" />
                  ) : row.value === "" ? (
                    <span className="text-muted-foreground">not set</span>
                  ) : (
                    row.value
                  )}
                </dd>
              </div>
            ))}
          </dl>
        </section>
        <section aria-label="Files" className="space-y-1">
          <h3 className="text-sm font-medium">Files</h3>
          <ul className="text-xs">
            {skill.files.map((f) => (
              <li key={f.path} className="flex justify-between gap-3 py-0.5">
                <span className="font-mono break-all">{f.path}</span>
                <span className="text-muted-foreground shrink-0">{formatBytes(f.size)}</span>
              </li>
            ))}
          </ul>
        </section>
        <section aria-label="SKILL.md" className="space-y-1">
          <h3 className="text-sm font-medium">SKILL.md</h3>
          <pre className="bg-muted/50 max-w-full overflow-x-auto rounded-md border p-3 font-mono text-xs break-words whitespace-pre-wrap">
            <Marked skill={skill} field="body" />
          </pre>
        </section>
      </article>
    </div>
  );
}
