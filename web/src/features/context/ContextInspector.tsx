/**
 * The Context inspector: one model request laid open in a large dialog. A
 * column lists the request's parts with what each costs; the pane beside it
 * shows the chosen part whole: the system prompt as the model reads it,
 * section by section, each tool with its parameters, every message, and
 * the parameters with the layer each came from. A part of the next request
 * links to the setting behind it, and the whole request copies as JSON.
 */

import { Check, Copy } from "lucide-react";
import { useState } from "react";

import type { ModelContext, ModelRequest, SessionConfiguration } from "@/api/types";
import { LoadError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Inspection } from "@/features/context/Inspection";
import { nextRequest } from "@/features/context/queries";
import type { RequestSelection } from "@/features/context/queries";
import { requestJSON } from "@/features/context/segments";
import type { View } from "@/features/context/segments";
import { formatAgo, formatTokens } from "@/lib/format";
import { cn } from "@/lib/utils";

export type ContextInspectorProps = {
  sessionId: string;
  chat: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  view: View;
  onView: (view: View) => void;
  requests: RequestSelection;
  config: SessionConfiguration | undefined;
};

export function ContextInspector({
  sessionId,
  chat,
  open,
  onOpenChange,
  view,
  onView,
  requests,
  config,
}: ContextInspectorProps) {
  const { shown } = requests;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex h-[min(92vh,56rem)] flex-col gap-0 p-0 sm:max-w-6xl">
        <DialogHeader className="border-b px-4 py-3 pr-12">
          <DialogTitle>Context inspector</DialogTitle>
          <DialogDescription>
            Everything one model request sends, part by part. Sizes are estimates, four bytes to a
            token.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-wrap items-center gap-2 border-b px-4 py-2">
          <RequestSelect id="inspector-request" requests={requests} className="w-full sm:w-80" />
          {shown.data !== undefined && <CopyButton context={shown.data} />}
        </div>
        {shown.isPending && (
          <div className="p-4">
            <Notice tone="pending">Assembling the request…</Notice>
          </div>
        )}
        {shown.isError && (
          <div className="p-4">
            <LoadError
              what={requests.selected === nextRequest ? "the next request" : "the recorded request"}
              error={shown.error}
              retrying={shown.isFetching}
              retry={() => void shown.refetch()}
            />
          </div>
        )}
        {shown.data !== undefined && (
          <Inspection
            sessionId={sessionId}
            chat={chat}
            context={shown.data}
            live={requests.selected === nextRequest}
            config={config}
            view={view}
            onView={onView}
            onEdit={() => {
              onOpenChange(false);
            }}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

export type RequestSelectProps = {
  id: string;
  requests: RequestSelection;
  className?: string;
};

/** RequestSelect picks the request shown: the next one, live, or a recorded call. */
export function RequestSelect({ id, requests, className }: RequestSelectProps) {
  const list = requests.records.data ?? [];
  return (
    <>
      <Select value={requests.selected} onValueChange={requests.select}>
        <SelectTrigger id={id} size="sm" aria-label="Request" className={cn("text-xs", className)}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={nextRequest} className="text-xs">
            Next request, live
          </SelectItem>
          {[...list].reverse().map((r, i) => (
            <SelectItem key={r.id} value={r.id} className="text-xs">
              {requestLabel(r, list.length - i)}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {requests.records.isError && (
        <LoadError
          what="the recorded requests"
          error={requests.records.error}
          retrying={requests.records.isFetching}
          retry={() => void requests.records.refetch()}
        />
      )}
    </>
  );
}

/** requestLabel names a recorded call in the select: its number, model, and measured input. */
function requestLabel(r: ModelRequest, n: number): string {
  const measured = r.input_tokens > 0 ? ` · ${formatTokens(r.input_tokens)} in` : "";
  return `#${String(n)} · ${r.model}${measured} · ${formatAgo(r.created_at)}`;
}

/** CopyButton copies the request as one JSON document. */
function CopyButton({ context }: { context: ModelContext }) {
  const [copied, setCopied] = useState(false);
  const [failed, setFailed] = useState(false);
  return (
    <div className="ml-auto flex items-center gap-2">
      {failed && <span className="text-destructive text-xs">Could not copy: no clipboard</span>}
      <Button
        type="button"
        size="sm"
        variant="outline"
        title="The request as the harness hands it to the provider, before the provider adapts it to its endpoint"
        onClick={() => {
          const text = JSON.stringify(requestJSON(context), null, 2);
          navigator.clipboard.writeText(text).then(
            () => {
              setFailed(false);
              setCopied(true);
              setTimeout(() => {
                setCopied(false);
              }, 2000);
            },
            () => {
              setFailed(true);
            },
          );
        }}
      >
        {copied ? <Check aria-hidden /> : <Copy aria-hidden />}
        {copied ? "Copied" : "Copy as JSON"}
      </Button>
    </div>
  );
}
