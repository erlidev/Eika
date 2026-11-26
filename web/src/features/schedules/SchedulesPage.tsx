import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router";

import { request, requestEmpty } from "@/api/client";
import { ActionError, LoadError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { Switch } from "@/components/ui/switch";

import { formatScheduleTime } from "@/features/schedules/time";
import type { Routine, Wakeup } from "@/features/schedules/types";

type Run = {
  id: string;
  session_id: string;
  scheduled_at: string;
  outcome: string;
  reason: string;
};
const path = (id: string) => `/api/routines/${encodeURIComponent(id)}`;

export function ScheduleActivity() {
  const routines = useQuery({
    queryKey: ["routines"],
    queryFn: ({ signal }) => request<{ routines: Routine[] }>("/api/routines", { signal }),
  });
  const wakes = useQuery({
    queryKey: ["wakeups"],
    queryFn: ({ signal }) => request<{ wakeups: Wakeup[] }>("/api/wakeups", { signal }),
  });
  return (
    <div className="border-t p-3 text-sm">
      <h2 className="font-semibold">Schedules</h2>
      {routines.isError || wakes.isError ? (
        <Notice>Could not load schedule activity. Open Schedules to retry.</Notice>
      ) : (
        <>
          {routines.data?.routines.map((r) => (
            <History key={r.id} routine={r} />
          ))}
          {wakes.data?.wakeups.map((w) => (
            <p key={w.id} className="mt-2 break-words">
              Wakeup: {w.agent} · {formatScheduleTime(w.due_at, "UTC")} · {w.state}{" "}
              <Link
                className="text-primary underline"
                to={`/activity/${encodeURIComponent(w.session_id)}`}
              >
                Session
              </Link>
            </p>
          ))}
          <Link className="text-primary mt-2 inline-block underline" to="/schedules">
            Manage schedules
          </Link>
        </>
      )}
    </div>
  );
}

function History({ routine }: { routine: Routine }) {
  const runs = useQuery({
    queryKey: ["routine-runs", routine.id],
    queryFn: ({ signal }) => request<{ runs: Run[] }>(`${path(routine.id)}/runs`, { signal }),
  });
  return (
    <section className="mt-2">
      <h3 className="font-medium">{routine.name} runs</h3>
      {runs.isPending ? (
        <p>Loading…</p>
      ) : runs.isError ? (
        <LoadError
          what="run history"
          error={runs.error}
          retry={() => void runs.refetch()}
          retrying={runs.isFetching}
        />
      ) : runs.data.runs.length === 0 ? (
        <p className="text-muted-foreground text-xs">No runs yet.</p>
      ) : (
        runs.data.runs.map((run) => (
          <p key={run.id} className="text-xs break-words">
            {formatScheduleTime(run.scheduled_at, routine.timezone)} · {run.outcome || "Running"}{" "}
            {run.reason}{" "}
            {run.session_id && (
              <Link
                className="text-primary underline"
                to={`/activity/${encodeURIComponent(run.session_id)}`}
              >
                Session
              </Link>
            )}
          </p>
        ))
      )}
    </section>
  );
}

export function SchedulesPage() {
  const client = useQueryClient();
  const routines = useQuery({
    queryKey: ["routines"],
    queryFn: ({ signal }) => request<{ routines: Routine[] }>("/api/routines", { signal }),
  });
  const wakes = useQuery({
    queryKey: ["wakeups"],
    queryFn: ({ signal }) => request<{ wakeups: Wakeup[] }>("/api/wakeups", { signal }),
  });
  const [editing, setEditing] = useState<Routine | "new" | null>(null);
  const action = useMutation({
    mutationFn: ({
      url,
      method,
      body,
    }: {
      url: string;
      method: "POST" | "PATCH" | "DELETE";
      body?: unknown;
    }) => requestEmpty(url, { method, body }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ["routines"] });
      await client.invalidateQueries({ queryKey: ["wakeups"] });
    },
  });
  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto max-w-4xl space-y-4 p-3 sm:p-4">
        <div className="flex items-center justify-between gap-2">
          <h1 className="text-lg font-semibold">Schedules</h1>
          <Button
            onClick={() => {
              setEditing("new");
            }}
          >
            New routine
          </Button>
        </div>
        {action.isError && <ActionError action="update the schedule" error={action.error} />}
        {routines.isPending ? (
          <Notice tone="pending">Loading routines…</Notice>
        ) : routines.isError ? (
          <LoadError
            what="routines"
            error={routines.error}
            retry={() => void routines.refetch()}
            retrying={routines.isFetching}
          />
        ) : (
          <>
            {routines.data.routines.length === 0 && (
              <p className="text-muted-foreground text-sm">
                No routines yet. Create one to schedule work.
              </p>
            )}
            {routines.data.routines
              .filter((r) => !r.approved_at)
              .map((r) => (
                <section key={r.id} className="space-y-2 rounded-md border p-3">
                  <h2 className="font-semibold">Approval needed: {r.name}</h2>
                  <Preview routine={r} timeZone={r.timezone} />
                  <p className="text-sm">Agent: {r.agent}</p>
                  {(r.warning.unscanned || (r.warning.spans?.length ?? 0) > 0) && (
                    <Notice tone="warning">
                      The prompt was flagged or could not be scanned. Review it before approval.
                    </Notice>
                  )}
                  <p className="text-sm break-words whitespace-pre-wrap">{r.prompt}</p>
                  <div className="flex flex-wrap gap-2">
                    <Button
                      disabled={action.isPending}
                      onClick={() => {
                        action.mutate({ url: `${path(r.id)}/approve`, method: "POST" });
                      }}
                    >
                      Approve
                    </Button>
                    <Button
                      variant="destructive"
                      disabled={action.isPending}
                      onClick={() => {
                        action.mutate({ url: path(r.id), method: "DELETE" });
                      }}
                    >
                      Delete
                    </Button>
                  </div>
                </section>
              ))}
            {routines.data.routines
              .filter((r) => r.approved_at)
              .map((r) => (
                <section key={r.id} className="space-y-2 rounded-md border p-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <h2 className="font-semibold">{r.name}</h2>
                    <div className="flex items-center gap-3">
                      <label className="flex items-center gap-2 text-sm">
                        <Switch
                          checked={r.enabled}
                          onCheckedChange={(enabled) => {
                            action.mutate({ url: path(r.id), method: "PATCH", body: { enabled } });
                          }}
                        />{" "}
                        Enabled
                      </label>
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => {
                          setEditing(r);
                        }}
                      >
                        Edit
                      </Button>
                      <Button
                        size="sm"
                        variant="destructive"
                        onClick={() => {
                          action.mutate({ url: path(r.id), method: "DELETE" });
                        }}
                      >
                        Delete
                      </Button>
                    </div>
                  </div>
                  <p className="text-sm">{r.schedule_words}</p>
                  <p className="text-muted-foreground text-xs">
                    Next: {formatScheduleTime(r.next_run_at, r.timezone)} · Last:{" "}
                    {r.last_outcome || "No runs yet"} {r.last_reason}
                  </p>
                  <History routine={r} />
                </section>
              ))}
          </>
        )}
        <section className="space-y-2">
          <h2 className="font-semibold">Wakeups</h2>
          {wakes.isPending ? (
            <Notice tone="pending">Loading wakeups…</Notice>
          ) : wakes.isError ? (
            <LoadError
              what="wakeups"
              error={wakes.error}
              retry={() => void wakes.refetch()}
              retrying={wakes.isFetching}
            />
          ) : wakes.data.wakeups.length === 0 ? (
            <p className="text-muted-foreground text-sm">No wakeups pending.</p>
          ) : (
            wakes.data.wakeups.map((w) => (
              <div
                key={w.id}
                className="flex flex-wrap items-center justify-between gap-2 rounded-md border p-3 text-sm"
              >
                <p className="break-words">
                  {w.agent} · {formatScheduleTime(w.due_at, "UTC")} · {w.state}
                  <br />
                  {w.note}
                </p>
                {w.state === "pending" && (
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={action.isPending}
                    onClick={() => {
                      action.mutate({
                        url: `/api/wakeups/${encodeURIComponent(w.id)}`,
                        method: "DELETE",
                      });
                    }}
                  >
                    Cancel
                  </Button>
                )}
              </div>
            ))
          )}
        </section>
        {editing !== null && (
          <Editor
            routine={editing === "new" ? undefined : editing}
            close={() => {
              setEditing(null);
            }}
          />
        )}
      </div>
    </div>
  );
}
function Preview({
  routine,
  timeZone,
}: {
  routine: Pick<Routine, "schedule_words" | "next_five">;
  timeZone: string;
}) {
  return (
    <div className="text-sm">
      <p>{routine.schedule_words}</p>
      <p className="text-muted-foreground mt-1 text-xs">Next times:</p>
      {routine.next_five.length ? (
        <ul className="text-xs">
          {routine.next_five.map((t) => (
            <li key={t}>{formatScheduleTime(t, timeZone)}</li>
          ))}
        </ul>
      ) : (
        <p className="text-xs">None</p>
      )}
    </div>
  );
}
function Editor({ routine, close }: { routine?: Routine; close: () => void }) {
  const client = useQueryClient();
  const [form, setForm] = useState({
    name: routine?.name ?? "",
    schedule_kind: routine?.schedule_kind ?? "cron",
    schedule_expr: routine?.schedule_expr ?? "",
    timezone: routine?.timezone ?? "UTC",
    agent: routine?.agent ?? "eika",
    prompt: routine?.prompt ?? "",
    enabled: routine?.enabled ?? true,
  });
  const preview = useQuery({
    queryKey: ["schedule-preview", form.schedule_kind, form.schedule_expr, form.timezone],
    queryFn: ({ signal }) =>
      request<Pick<Routine, "schedule_words" | "next_five">>("/api/routines/preview", {
        method: "POST",
        body: {
          schedule_kind: form.schedule_kind,
          schedule_expr: form.schedule_expr,
          timezone: form.timezone,
        },
        signal,
      }),
    enabled: form.schedule_expr.trim() !== "" && form.timezone.trim() !== "",
    retry: false,
  });
  const save = useMutation({
    mutationFn: () =>
      request<Routine>(routine ? path(routine.id) : "/api/routines", {
        method: routine ? "PATCH" : "POST",
        body: form,
      }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ["routines"] });
      close();
    },
  });
  const field = (
    key: "name" | "schedule_expr" | "timezone" | "agent" | "prompt",
    label: string,
  ) => (
    <label className="flex flex-col gap-1 text-sm">
      {label}
      {key === "prompt" ? (
        <textarea
          required
          className="min-h-24 rounded-md border p-2"
          value={form[key]}
          onChange={(e) => {
            setForm({ ...form, [key]: e.target.value });
          }}
        />
      ) : (
        <input
          required
          className="rounded-md border p-2"
          value={form[key]}
          onChange={(e) => {
            setForm({ ...form, [key]: e.target.value });
          }}
        />
      )}
    </label>
  );
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
    >
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-lg">
        <DialogTitle>{routine ? "Edit routine" : "New routine"}</DialogTitle>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          {field("name", "Name")}
          <label className="flex flex-col gap-1 text-sm">
            Schedule type
            <select
              className="rounded-md border p-2"
              value={form.schedule_kind}
              onChange={(e) => {
                setForm({ ...form, schedule_kind: e.target.value });
              }}
            >
              <option value="cron">Cron</option>
              <option value="at">One time</option>
            </select>
          </label>
          {field(
            "schedule_expr",
            form.schedule_kind === "cron" ? "Cron expression (five fields)" : "Time (RFC 3339)",
          )}
          {field("timezone", "Time zone")}
          {field("agent", "Agent")}
          {field("prompt", "Prompt")}
          {preview.isFetching ? (
            <Notice tone="pending">Checking the schedule…</Notice>
          ) : preview.isError ? (
            <LoadError
              what="the schedule preview"
              error={preview.error}
              retry={() => void preview.refetch()}
              retrying={preview.isFetching}
            />
          ) : preview.data ? (
            <Preview routine={preview.data} timeZone={form.timezone} />
          ) : (
            <p className="text-muted-foreground text-xs">
              Enter a schedule and time zone to see the next times.
            </p>
          )}
          {save.isError && <ActionError action="save the routine" error={save.error} />}
          <Button type="submit" disabled={save.isPending}>
            Save
          </Button>
        </form>
      </DialogContent>
    </Dialog>
  );
}
