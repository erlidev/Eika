/**
 * What an MCP server asks the user during a tool call: a form to fill in,
 * or a page to visit. The call waits until the user accepts, declines, or
 * cancels; the server reads which, and the content of an accepted form.
 */

import { ExternalLink } from "lucide-react";
import { useState } from "react";

import type { Elicitation, ElicitationAnswer } from "@/api/types";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { safeHref } from "@/features/search";
import { formContent, formFields, initialValues } from "@/features/session/elicitation";
import type { Field, Value } from "@/features/session/elicitation";
import { useAnswerElicitation } from "@/features/session/queries";
import { useSessionStore } from "@/features/session/store";
import { failureText } from "@/lib/failure";

export type ElicitationFormProps = {
  elicitation: Elicitation;
};

export function ElicitationForm({ elicitation }: ElicitationFormProps) {
  const sessionId = useSessionStore((s) => s.sessionId);
  const dismiss = useSessionStore((s) => s.dismissElicitation);
  const answer = useAnswerElicitation(sessionId);
  // The call continues on the first answer, so a second is refused.
  const [submitted, setSubmitted] = useState(false);

  const send = (reply: ElicitationAnswer) => {
    if (submitted) return;
    setSubmitted(true);
    answer.mutate(
      { id: elicitation.id, answer: reply },
      {
        onSuccess: () => {
          dismiss(elicitation.id);
        },
        onError: () => {
          setSubmitted(false);
        },
      },
    );
  };
  const busy = answer.isPending || submitted;

  return (
    <section
      aria-label={`${elicitation.server} asks for input`}
      className="border-primary/40 bg-primary/5 space-y-3 rounded-md border p-3"
    >
      <p className="text-muted-foreground text-xs">
        <span className="font-mono">{elicitation.server}</span> asks:
      </p>
      <p className="text-sm font-medium whitespace-pre-wrap">{elicitation.message}</p>
      {elicitation.mode === "url" ? (
        <UrlRequest elicitation={elicitation} busy={busy} send={send} />
      ) : (
        <FormRequest elicitation={elicitation} busy={busy} send={send} />
      )}
      {answer.isError && (
        <p className="text-destructive text-xs" role="alert">
          {failureText("send the answer", answer.error)}
        </p>
      )}
    </section>
  );
}

type RequestProps = {
  elicitation: Elicitation;
  busy: boolean;
  send: (reply: ElicitationAnswer) => void;
};

/** Refuse is the two ways out of any request: decline it, or cancel the question. */
function Refuse({ busy, send }: Pick<RequestProps, "busy" | "send">) {
  return (
    <>
      <Button
        type="button"
        size="sm"
        variant="outline"
        disabled={busy}
        onClick={() => {
          send({ action: "decline" });
        }}
      >
        Decline
      </Button>
      <Button
        type="button"
        size="sm"
        variant="ghost"
        disabled={busy}
        onClick={() => {
          send({ action: "cancel" });
        }}
      >
        Cancel
      </Button>
    </>
  );
}

/**
 * UrlRequest sends the user to a page, where the server does what it could
 * not do in a form, such as a sign-in. Accepting says they went.
 */
function UrlRequest({ elicitation, busy, send }: RequestProps) {
  const href = safeHref(elicitation.url ?? "");
  const [opened, setOpened] = useState(false);
  const host = hostOf(elicitation.url ?? "");
  return (
    <div className="space-y-2">
      <p className="text-muted-foreground font-mono text-xs break-all">{elicitation.url}</p>
      <p className="text-muted-foreground text-xs">
        The page opens in a new tab. Check that you expect to be sent to{" "}
        <span className="text-foreground font-mono">{host}</span> before you go.
      </p>
      <div className="flex flex-wrap gap-2">
        {href !== undefined && (
          <Button asChild size="sm" variant={opened ? "outline" : "default"}>
            <a
              href={href}
              target="_blank"
              rel="noreferrer noopener"
              onClick={() => {
                setOpened(true);
              }}
            >
              <ExternalLink aria-hidden />
              Open {host}
            </a>
          </Button>
        )}
        <Button
          type="button"
          size="sm"
          variant={opened ? "default" : "outline"}
          disabled={busy}
          onClick={() => {
            send({ action: "accept" });
          }}
        >
          Done
        </Button>
        <Refuse busy={busy} send={send} />
      </div>
    </div>
  );
}

/** hostOf is the host a URL sends the user to, empty for one that does not parse. */
function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return "";
  }
}

/** FormRequest is a form of the fields the server's schema describes. */
function FormRequest({ elicitation, busy, send }: RequestProps) {
  const fields = formFields(elicitation.requested_schema) ?? [];
  const [values, setValues] = useState(() => initialValues(elicitation.requested_schema, fields));
  const [tried, setTried] = useState(false);
  const outcome = formContent(fields, values);
  const problem = "problem" in outcome ? outcome : undefined;
  const set = (name: string, value: Value) => {
    setValues((v) => ({ ...v, [name]: value }));
  };
  return (
    <form
      className="space-y-3"
      noValidate
      onSubmit={(e) => {
        e.preventDefault();
        setTried(true);
        if ("content" in outcome) send({ action: "accept", content: outcome.content });
      }}
    >
      {fields.map((f) => (
        <FieldInput
          key={f.name}
          id={`elicit-${elicitation.id}-${f.name}`}
          field={f}
          value={values[f.name] ?? ""}
          problem={tried && problem?.field === f.name ? problem.problem : undefined}
          onChange={(value) => {
            set(f.name, value);
          }}
        />
      ))}
      <div className="flex flex-wrap gap-2">
        <Button type="submit" size="sm" disabled={busy}>
          Submit
        </Button>
        <Refuse busy={busy} send={send} />
      </div>
    </form>
  );
}

type FieldInputProps = {
  id: string;
  field: Field;
  value: Value;
  problem: string | undefined;
  onChange: (value: Value) => void;
};

function FieldInput({ id, field, value, problem, onChange }: FieldInputProps) {
  const label = (
    <Label htmlFor={id} className="text-xs">
      {field.label}
      {field.required ? "" : <span className="text-muted-foreground"> (optional)</span>}
    </Label>
  );
  const describedBy = [
    field.description === undefined ? "" : `${id}-description`,
    problem === undefined ? "" : `${id}-problem`,
  ]
    .filter((x) => x !== "")
    .join(" ");
  const notes = (
    <>
      {field.description !== undefined && (
        <p id={`${id}-description`} className="text-muted-foreground text-xs">
          {field.description}
        </p>
      )}
      {problem !== undefined && (
        <p id={`${id}-problem`} className="text-destructive text-xs">
          {problem}
        </p>
      )}
    </>
  );
  const common = {
    id,
    "aria-invalid": problem !== undefined,
    ...(describedBy === "" ? {} : { "aria-describedby": describedBy }),
  };

  switch (field.kind) {
    case "boolean":
      return (
        <div className="space-y-1">
          <div className="flex items-center gap-2">
            <Switch
              {...common}
              checked={value === true}
              onCheckedChange={(on) => {
                onChange(on);
              }}
            />
            {label}
          </div>
          {notes}
        </div>
      );
    case "choice":
      return (
        <div className="space-y-1">
          {label}
          <Select
            value={typeof value === "string" ? value : ""}
            onValueChange={(v) => {
              onChange(v);
            }}
          >
            <SelectTrigger {...common} size="sm" className="w-full sm:w-72">
              <SelectValue placeholder="Choose one" />
            </SelectTrigger>
            <SelectContent>
              {(field.options ?? []).map((o) => (
                <SelectItem key={o.value} value={o.value}>
                  {o.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {notes}
        </div>
      );
    case "multi": {
      const chosen = Array.isArray(value) ? value : [];
      return (
        <fieldset className="space-y-1" aria-describedby={describedBy || undefined}>
          <legend className="text-xs font-medium">
            {field.label}
            {field.required ? "" : <span className="text-muted-foreground"> (optional)</span>}
          </legend>
          <div className="flex flex-wrap gap-x-4 gap-y-1">
            {(field.options ?? []).map((o) => (
              <div key={o.value} className="flex items-center gap-1.5">
                <Checkbox
                  id={`${id}-${o.value}`}
                  checked={chosen.includes(o.value)}
                  onCheckedChange={(on) => {
                    onChange(
                      on === true ? [...chosen, o.value] : chosen.filter((x) => x !== o.value),
                    );
                  }}
                />
                <Label htmlFor={`${id}-${o.value}`} className="text-xs font-normal">
                  {o.label}
                </Label>
              </div>
            ))}
          </div>
          {notes}
        </fieldset>
      );
    }
    case "list":
      return (
        <div className="space-y-1">
          {label}
          <Textarea
            {...common}
            rows={3}
            value={Array.isArray(value) ? value.join("\n") : ""}
            placeholder="One per line"
            className="text-sm"
            onChange={(e) => {
              onChange(e.target.value.split("\n"));
            }}
          />
          {notes}
        </div>
      );
    default:
      return (
        <div className="space-y-1">
          {label}
          <Input
            {...common}
            value={typeof value === "string" ? value : ""}
            autoComplete="off"
            inputMode={field.kind === "text" ? undefined : "decimal"}
            type={
              field.format === "email"
                ? "email"
                : field.format === "uri"
                  ? "url"
                  : field.format === "date"
                    ? "date"
                    : "text"
            }
            className="h-8"
            onChange={(e) => {
              onChange(e.target.value);
            }}
          />
          {notes}
        </div>
      );
  }
}
