/** Settings, Security: each tripwire's switch with how often it fired, and the numbers behind the marks. */

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { getTripwireCounts } from "@/api/routes";
import { queryKeys } from "@/api/keys";
import type { SinkLimits, TripwireName, TripwireSettings } from "@/api/types";
import { ActionError, LoadError } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { tripwireTitle } from "@/features/inbox/state";
import { useSaveSettings, useSettings } from "@/features/settings/queries";

const tripwireOrder: TripwireName[] = [
  "followed",
  "carried_out",
  "lifted",
  "drained",
  "blocked_often",
];

/** parseWhole reads a whole number of at least 1, or undefined. */
function parseWhole(text: string): number | undefined {
  const n = Number(text);
  return text.trim() !== "" && Number.isInteger(n) && n >= 1 ? n : undefined;
}

type Draft = {
  watch: string;
  list: string;
  upload: string;
  ratioBytes: string;
  ratio: string;
  fanOut: string;
  blocked: string;
};

export function TripwireSettingsSection() {
  const settings = useSettings();
  const counts = useQuery({
    queryKey: queryKeys.tripwireCounts(),
    queryFn: ({ signal }) => getTripwireCounts(signal),
  });
  const save = useSaveSettings();
  const [draft, setDraft] = useState<Partial<Draft>>({});
  const state = settings.data;
  if (settings.isError) {
    return (
      <LoadError
        what="tripwire settings"
        error={settings.error}
        retry={() => void settings.refetch()}
        retrying={settings.isFetching}
      />
    );
  }
  if (!state) return null;
  // A stored object that leaves a field out keeps that field's default.
  const tripwires: TripwireSettings = {
    ...state.defaults.tripwires,
    ...(state.settings.tripwires as Partial<TripwireSettings> | null | undefined),
  };
  const sinks: SinkLimits = {
    ...state.defaults.sink_limits,
    ...(state.settings.sink_limits as Partial<SinkLimits> | null | undefined),
  };
  const watch =
    typeof state.settings.watch_threshold === "number"
      ? state.settings.watch_threshold
      : state.defaults.watch_threshold;
  const list = Array.isArray(state.settings.sensitive_list)
    ? (state.settings.sensitive_list as string[])
    : state.defaults.sensitive_list;
  const value: Draft = {
    watch: draft.watch ?? String(watch),
    list: draft.list ?? list.join("\n"),
    upload: draft.upload ?? String(sinks.upload_bytes),
    ratioBytes: draft.ratioBytes ?? String(sinks.upload_ratio_bytes),
    ratio: draft.ratio ?? String(sinks.upload_ratio),
    fanOut: draft.fanOut ?? String(sinks.fan_out_urls),
    blocked: draft.blocked ?? String(tripwires.blocked_limit),
  };
  const set = (key: keyof Draft) => (event: { target: { value: string } }) => {
    setDraft({ ...draft, [key]: event.target.value });
  };
  const watchNumber = Number(value.watch);
  const entries = value.list.split("\n").filter((line) => line.trim() !== "");
  const whole = {
    upload_bytes: parseWhole(value.upload),
    upload_ratio_bytes: parseWhole(value.ratioBytes),
    upload_ratio: parseWhole(value.ratio),
    fan_out_urls: parseWhole(value.fanOut),
    blocked_limit: parseWhole(value.blocked),
  };
  const valid =
    value.watch.trim() !== "" &&
    watchNumber > 0 &&
    watchNumber <= 1 &&
    Object.values(whole).every((n) => n !== undefined);
  const fired = new Map(counts.data?.counts.map((c) => [c.tripwire, c]));
  return (
    <section className="space-y-3">
      <div className="space-y-1">
        <h3 className="text-sm font-medium">Tripwires</h3>
        <p className="text-muted-foreground text-xs">
          A tripwire fires when a chain of marks on one run completes: it holds the run, adds an
          entry to the Inbox and sends one push. All are off until you switch them on.
        </p>
      </div>
      <div className="space-y-2">
        {tripwireOrder.map((name) => {
          const count = fired.get(name);
          return (
            <div
              key={name}
              className="flex items-center justify-between gap-3 rounded-md border p-2"
            >
              <div className="min-w-0 space-y-0.5">
                <Label htmlFor={`tripwire-${name}`}>{tripwireTitle[name]}</Label>
                <p className="text-muted-foreground text-xs">
                  Fired {count?.fired ?? 0}, this was fine {count?.fine ?? 0}
                </p>
              </div>
              <Switch
                id={`tripwire-${name}`}
                checked={tripwires[name]}
                disabled={save.isPending}
                onCheckedChange={(checked) => {
                  save.mutate({ tripwires: { ...tripwires, [name]: checked } });
                }}
              />
            </div>
          );
        })}
      </div>
      {counts.isError && (
        <LoadError
          what="tripwire counts"
          error={counts.error}
          retry={() => void counts.refetch()}
          retrying={counts.isFetching}
        />
      )}
      <form
        className="space-y-3"
        onSubmit={(event) => {
          event.preventDefault();
          if (!valid) return;
          save.mutate(
            {
              watch_threshold: watchNumber,
              sensitive_list: entries,
              sink_limits: {
                upload_bytes: whole.upload_bytes,
                upload_ratio_bytes: whole.upload_ratio_bytes,
                upload_ratio: whole.upload_ratio,
                fan_out_urls: whole.fan_out_urls,
              },
              tripwires: { ...tripwires, blocked_limit: whole.blocked_limit },
            },
            {
              onSuccess: () => {
                setDraft({});
              },
            },
          );
        }}
      >
        <Field
          id="watch-threshold"
          label="Watch threshold"
          value={value.watch}
          onChange={set("watch")}
          step="any"
          hint="A page scoring at or above this has the hosts it names watched. Above 0, at most 1."
        />
        <Field
          id="upload-bytes"
          label="Upload bytes"
          value={value.upload}
          onChange={set("upload")}
          hint="Bytes sent to one new host in one run before an upload mark."
        />
        <Field
          id="upload-ratio-bytes"
          label="Lopsided upload bytes"
          value={value.ratioBytes}
          onChange={set("ratioBytes")}
          hint="Bytes sent before the ratio below counts."
        />
        <Field
          id="upload-ratio"
          label="Upload ratio"
          value={value.ratio}
          onChange={set("ratio")}
          hint="Times more sent than received."
        />
        <Field
          id="fan-out-urls"
          label="Fan-out URLs"
          value={value.fanOut}
          onChange={set("fanOut")}
          hint="Different URLs one run may fetch on a host only content named."
        />
        <Field
          id="blocked-limit"
          label="Blocked limit"
          value={value.blocked}
          onChange={set("blocked")}
          hint="Blocked results in one run that fire Blocked many times."
        />
        <div className="space-y-1">
          <Label htmlFor="sensitive-list">Sensitive reads</Label>
          <Textarea
            id="sensitive-list"
            className="font-mono text-xs"
            rows={5}
            value={value.list}
            onChange={set("list")}
          />
          <p className="text-muted-foreground text-xs">
            One regular expression per line, matched against a file path or a command; a match marks
            a sensitive read.
          </p>
        </div>
        <Button type="submit" disabled={!valid || save.isPending}>
          Save numbers
        </Button>
      </form>
      {save.isError && <ActionError action="save tripwire settings" error={save.error} />}
    </section>
  );
}

type FieldProps = {
  id: string;
  label: string;
  value: string;
  onChange: (event: { target: { value: string } }) => void;
  hint: string;
  step?: string;
};

function Field({ id, label, value, onChange, hint, step }: FieldProps) {
  return (
    <div className="space-y-1">
      <Label htmlFor={id}>{label}</Label>
      <Input id={id} type="number" min="0" step={step} value={value} onChange={onChange} />
      <p className="text-muted-foreground text-xs">{hint}</p>
    </div>
  );
}
