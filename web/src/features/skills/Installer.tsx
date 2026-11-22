/** The install area: a drop zone and a file picker for one zip, and what the install answered. */

import { Upload } from "lucide-react";
import { useRef, useState } from "react";
import { Link } from "react-router";

import { ActionError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { useInstallSkill } from "@/features/skills/queries";
import { summarize } from "@/features/skills/state";
import { cn } from "@/lib/utils";

export function Installer() {
  const install = useInstallSkill();
  const picker = useRef<HTMLInputElement>(null);
  const [over, setOver] = useState(false);

  const send = (files: readonly File[]) => {
    const [file] = files;
    if (file !== undefined) install.mutate(file);
  };

  return (
    <div className="space-y-2 border-b p-3">
      <div
        data-testid="skill-drop"
        className={cn(
          "flex flex-col items-center gap-1.5 rounded-md border border-dashed px-3 py-4 text-center text-xs transition-colors",
          over && "border-ring bg-accent",
        )}
        onDragOver={(e) => {
          e.preventDefault();
          setOver(true);
        }}
        onDragLeave={() => {
          setOver(false);
        }}
        onDrop={(e) => {
          e.preventDefault();
          setOver(false);
          send([...e.dataTransfer.files]);
        }}
      >
        <Upload aria-hidden className="text-muted-foreground size-4" />
        <p className="text-muted-foreground">Drop a skill zip here, or</p>
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={install.isPending}
          onClick={() => picker.current?.click()}
        >
          {install.isPending ? "Installing…" : "Choose a zip"}
        </Button>
        <input
          ref={picker}
          type="file"
          hidden
          accept=".zip,application/zip"
          aria-label="Skill zip"
          onChange={(e) => {
            const files = [...(e.target.files ?? [])];
            // The same file chosen twice must fire change again.
            e.target.value = "";
            send(files);
          }}
        />
      </div>
      {install.isError && <ActionError action="install the skill" error={install.error} />}
      {install.isSuccess && <Result result={install.data} />}
    </div>
  );
}

function Result({ result }: { result: Parameters<typeof summarize>[0] }) {
  const s = summarize(result);
  return (
    <Notice tone={s.tone}>
      <p>{s.text}</p>
      {s.details.length > 0 && (
        <ul className="mt-1 list-disc space-y-0.5 pl-4">
          {s.details.map((d) => (
            <li key={d}>{d}</li>
          ))}
        </ul>
      )}
      <Link
        to={`/skills/${encodeURIComponent(result.skill.name)}`}
        className="mt-1 inline-block underline underline-offset-2"
      >
        Review {result.skill.name}
      </Link>
    </Notice>
  );
}
