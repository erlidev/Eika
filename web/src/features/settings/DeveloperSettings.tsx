/** Inspect the next request and the recorded calls of a session. */
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { queryKeys } from "@/api/keys";
import type { ModelContext, Session } from "@/api/types";
import { listSessions } from "@/api/routes";
import { LoadError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { ContextInspector, RequestSelect, useRequestSelection } from "@/features/context";
import type { View } from "@/features/context";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useSessionStore } from "@/features/session";
import { modelClassAssignments, useSettings } from "@/features/settings/queries";
import { useSettingsDialog } from "@/features/settings/store";
import { formatTokens } from "@/lib/format";

export function DeveloperSettings() {
  const [session, setSession] = useState("");
  const sessions = useQuery({
    queryKey: queryKeys.allSessions(),
    queryFn: ({ signal }) => listSessions(signal),
  });
  const selectedSession = sessions.data?.find((item) => item.id === session);
  return (
    <div className="space-y-4">
      <div className="space-y-1.5">
        <Label htmlFor="developer-session">Session</Label>
        <Select
          value={session}
          onValueChange={(value) => {
            setSession(value);
          }}
        >
          <SelectTrigger id="developer-session" className="w-full">
            <SelectValue placeholder="Choose a session" />
          </SelectTrigger>
          <SelectContent>
            {sessions.data?.map((item) => (
              <SelectItem key={item.id} value={item.id}>
                {item.title || item.id}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {sessions.isError && (
          <LoadError
            what="the sessions"
            error={sessions.error}
            retrying={sessions.isFetching}
            retry={() => void sessions.refetch()}
          />
        )}
      </div>
      {selectedSession && <SessionRequests key={session} session={selectedSession} />}
    </div>
  );
}

function SessionRequests({ session }: { session: Session }) {
  const settings = useSettings();
  const chosen = useSessionStore((state) =>
    state.sessionId === session.id ? state.model : (state.parked[session.id]?.model ?? ""),
  );
  const model = session.agent ? "" : chosen || (modelClassAssignments(settings.data).main ?? "");
  const canPreview =
    settings.data !== undefined &&
    ((session.agent !== undefined && session.agent !== "") || model !== "");
  const requests = useRequestSelection(session.id, model, canPreview);
  const showSettings = useSettingsDialog((s) => s.show);
  const [open, setOpen] = useState(false);
  const [view, setView] = useState<View>("system");
  return (
    <div className="space-y-3">
      <Label htmlFor="developer-call">Request</Label>
      <RequestSelect id="developer-call" requests={requests} className="w-full" />
      {settings.isError && (
        <LoadError
          what="the settings"
          error={settings.error}
          retrying={settings.isFetching}
          retry={() => void settings.refetch()}
        />
      )}
      {!canPreview && requests.selected === "next" && !settings.isError && (
        <Notice>
          {settings.isPending
            ? "Loading the settings…"
            : "Choose a model for the main class in Settings, General."}
        </Notice>
      )}
      {requests.shown.isPending && (canPreview || requests.selected !== "next") && (
        <Notice tone="pending">Loading the request…</Notice>
      )}
      {requests.shown.isError && (
        <LoadError
          what="the request"
          error={requests.shown.error}
          retrying={requests.shown.isFetching}
          retry={() => void requests.shown.refetch()}
        />
      )}
      {requests.shown.data && (
        <>
          <RequestDetails context={requests.shown.data} />
          <Button
            onClick={() => {
              setOpen(true);
            }}
          >
            Inspect request
          </Button>
        </>
      )}
      <ContextInspector
        open={open}
        onOpenChange={setOpen}
        view={view}
        onView={setView}
        requests={requests}
        onSettings={(tab) => {
          setOpen(false);
          showSettings(tab);
        }}
      />
    </div>
  );
}

function RequestDetails({ context }: { context: ModelContext }) {
  const call = context.call;
  const count = (n: number | null | undefined) =>
    n == null ? "Unknown" : n < 0 ? `-${formatTokens(-n)}` : formatTokens(n);
  return (
    <div className="space-y-3 text-sm">
      <dl className="grid grid-cols-2 gap-1 rounded-md border p-3">
        <dt>Model</dt>
        <dd className="font-mono">{context.parameters.model || "None"}</dd>
        <dt>Context window</dt>
        <dd className="font-mono">{count(context.context_window)}</dd>
        <dt>Prompt</dt>
        <dd className="font-mono">
          {count(context.breakdown.prompt_tokens)} ({context.breakdown.prompt_source ?? "unknown"})
        </dd>
        <dt>Parts</dt>
        <dd className="font-mono">{count(context.breakdown.parts)}</dd>
        <dt>Remainder</dt>
        <dd className="font-mono">{count(context.breakdown.remainder)}</dd>
        {context.prediction && (
          <>
            <dt>Prediction</dt>
            <dd className="font-mono">
              {count(context.prediction.tokens)} ({context.prediction.method})
            </dd>
          </>
        )}
        {call && (
          <>
            <dt>Prediction</dt>
            <dd className="font-mono">
              {count(call.predicted_prompt_tokens)} ({call.prediction ?? "unknown"})
            </dd>
            <dt>Drift</dt>
            <dd className="font-mono">{count(call.drift)}</dd>
            <dt>Completion</dt>
            <dd className="font-mono">{count(call.completion_tokens)}</dd>
            <dt>Cached</dt>
            <dd className="font-mono">{count(call.cached_tokens)}</dd>
            <dt>Reasoning</dt>
            <dd className="font-mono">{count(call.reasoning_tokens)}</dd>
          </>
        )}
      </dl>
      <p className="text-muted-foreground text-xs">
        Drift is the predicted prompt count less the measured count. A nonzero drift for an exact
        prediction indicates an error.
      </p>
      {(
        [
          ["System sections", context.sections],
          ["Tools", context.tools],
          ["Messages", context.messages],
          ["Parameters", context.parameters],
          ["Token breakdown", context.breakdown],
          ["Call record", context.call],
        ] as const
      )
        .filter(([, value]) => value !== undefined)
        .map(([label, value]) => (
          <details key={label} className="rounded-md border p-3">
            <summary>{label}</summary>
            <pre className="mt-2 overflow-auto whitespace-pre-wrap break-words font-mono text-xs">
              {JSON.stringify(value, null, 2)}
            </pre>
          </details>
        ))}
    </div>
  );
}
