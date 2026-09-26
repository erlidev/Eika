/** The search quotas: requests a provider may take per day or month, to stay in a free tier. */

import { useState } from "react";

import type { SearchLimit, SettingsState } from "@/api/types";
import { ActionError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  type LimitField,
  limitProblem,
  maxLimit,
  parseLimit,
  readLimits,
  searchSettingKeys,
} from "@/features/search";
import type { useSaveSettings } from "@/features/settings/queries";

type SaveSettings = ReturnType<typeof useSaveSettings>;

type QuotaFields = { day: LimitField; month: LimitField };

type QuotasProps = { state: SettingsState; save: SaveSettings };

export function Quotas({ state, save }: QuotasProps) {
  const stored = readLimits(state);
  const [fields, setFields] = useState<Record<string, QuotaFields>>(() =>
    Object.fromEntries(
      Object.entries(stored).map(([bucket, l]) => [
        bucket,
        {
          day: { text: l.day ? String(l.day) : "" },
          month: { text: l.month ? String(l.month) : "" },
        },
      ]),
    ),
  );
  const buckets = Object.keys(stored).sort();
  const invalid = Object.values(fields).some(
    (f) => limitProblem(f.day) !== null || limitProblem(f.month) !== null,
  );

  return (
    <form
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (invalid) return;
        const limits: Record<string, SearchLimit> = {};
        for (const [bucket, f] of Object.entries(fields)) {
          const day = parseLimit(f.day.text);
          const month = parseLimit(f.month.text);
          limits[bucket] = {
            ...(day === undefined ? {} : { day }),
            ...(month === undefined ? {} : { month }),
          };
        }
        save.mutate({ [searchSettingKeys.limits]: limits });
      }}
    >
      <div>
        <h3 className="text-sm font-medium">Quotas</h3>
        <p className="text-muted-foreground text-xs">
          Requests a provider may take per UTC day or month before the chain skips it, to stay
          inside a free tier. Enter a whole number from 1 to {maxLimit.toLocaleString("en-US")}, or
          leave a field empty for unlimited. Counts survive a restart.
        </p>
      </div>
      <div className="grid grid-cols-[auto_1fr_1fr] items-start gap-x-3 gap-y-2">
        <span />
        <span className="text-muted-foreground text-xs">Per day</span>
        <span className="text-muted-foreground text-xs">Per month</span>
        {buckets.map((bucket) => (
          <QuotaRow
            key={bucket}
            bucket={bucket}
            value={fields[bucket] ?? { day: { text: "" }, month: { text: "" } }}
            onChange={(value) => {
              setFields({ ...fields, [bucket]: value });
            }}
          />
        ))}
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <Button type="submit" variant="outline" disabled={invalid || save.isPending}>
          Save quotas
        </Button>
        {invalid && (
          <span className="text-destructive text-xs">Fix the quotas marked above to save.</span>
        )}
      </div>
      {save.isSuccess && <Notice tone="success">Saved. The next search uses these quotas.</Notice>}
      {save.isError && <ActionError action="save the quotas" error={save.error} />}
    </form>
  );
}

type QuotaRowProps = {
  bucket: string;
  value: QuotaFields;
  onChange: (value: QuotaFields) => void;
};

function QuotaRow({ bucket, value, onChange }: QuotaRowProps) {
  return (
    <>
      <span className="pt-2 font-mono text-sm">{bucket}</span>
      <QuotaInput
        label={`${bucket} per day`}
        id={`quota-${bucket}-day`}
        field={value.day}
        onChange={(day) => {
          onChange({ ...value, day });
        }}
      />
      <QuotaInput
        label={`${bucket} per month`}
        id={`quota-${bucket}-month`}
        field={value.month}
        onChange={(month) => {
          onChange({ ...value, month });
        }}
      />
    </>
  );
}

type QuotaInputProps = {
  label: string;
  id: string;
  field: LimitField;
  onChange: (field: LimitField) => void;
};

/** QuotaInput is one quota field with its problem, if any, right below it. */
function QuotaInput({ label, id, field, onChange }: QuotaInputProps) {
  const problem = limitProblem(field);
  return (
    <div className="space-y-1">
      <Input
        id={id}
        aria-label={label}
        type="number"
        inputMode="numeric"
        min={1}
        max={maxLimit}
        step={1}
        value={field.text}
        placeholder="Unlimited"
        aria-invalid={problem !== null}
        aria-describedby={problem ? `${id}-problem` : undefined}
        onChange={(e) => {
          onChange({ text: e.target.value, badInput: e.target.validity.badInput });
        }}
      />
      {problem && (
        <p id={`${id}-problem`} className="text-destructive text-xs">
          {problem}
        </p>
      )}
    </div>
  );
}
