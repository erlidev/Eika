/**
 * The form that configures an MCP server: a remote one by its URL, with the
 * headers every request carries and an OAuth client registered by hand if its
 * authorization server needs one, or a stdio one by the command that starts
 * it in a workspace. Values of headers and variables are never shown again,
 * so a stored one is left blank to keep it.
 */

import { ChevronRight, Plus, Trash2 } from "lucide-react";
import { useState } from "react";

import type { MCPServer, MCPServerKind } from "@/api/types";
import { ActionError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  createInput,
  droppedHeaders,
  emptyForm,
  formFromServer,
  maxServerName,
  updateInput,
  urlWillDropSecrets,
  validate,
} from "@/features/mcp/form";
import type { MCPField, MCPFormState, Pair } from "@/features/mcp/form";
import { useCreateMCPServer, useMCPServers, useUpdateMCPServer } from "@/features/mcp/queries";
import { cn } from "@/lib/utils";

export type MCPServerFormProps = {
  /** server is the server being changed; absent for a new one. */
  server?: MCPServer;
  /** onSaved receives the server as the harness stored it. */
  onSaved: (server: MCPServer) => void;
  onCancel: () => void;
};

const kinds: { kind: MCPServerKind; name: string; blurb: string }[] = [
  {
    kind: "http",
    name: "Remote (HTTP)",
    blurb: "A server at a URL. The harness connects to it, and chats can use it too.",
  },
  {
    kind: "stdio",
    name: "Local command (stdio)",
    blurb: "A program started in each workspace whose session uses it, such as npx or uvx.",
  },
];

export function MCPServerForm({ server, onSaved, onCancel }: MCPServerFormProps) {
  const servers = useMCPServers();
  const create = useCreateMCPServer();
  const update = useUpdateMCPServer();
  const editing = server !== undefined;
  const [form, setForm] = useState<MCPFormState>(() =>
    server ? formFromServer(server) : emptyForm(),
  );
  const takenNames = (servers.data ?? []).filter((s) => s.id !== server?.id).map((s) => s.name);
  const problem = validate(form, takenNames);
  const problemFor = (field: MCPField) => (problem?.field === field ? problem.message : null);
  const set = (patch: Partial<MCPFormState>) => {
    setForm((f) => ({ ...f, ...patch }));
  };
  const dropped = droppedHeaders(server, form);
  const moved = urlWillDropSecrets(server, form);

  const save = (e: React.SyntheticEvent) => {
    e.preventDefault();
    if (problem) return;
    if (editing) {
      update.mutate({ id: server.id, input: updateInput(server, form) }, { onSuccess: onSaved });
    } else {
      create.mutate(createInput(form), { onSuccess: onSaved });
    }
  };
  const saving = create.isPending || update.isPending;
  const saveError = create.error ?? update.error;

  return (
    <form className="space-y-5" onSubmit={save}>
      {!editing && (
        <fieldset className="space-y-2">
          <legend className="text-sm font-medium">Kind</legend>
          <div className="grid gap-2 sm:grid-cols-2">
            {kinds.map((option) => (
              <button
                key={option.kind}
                type="button"
                aria-pressed={form.kind === option.kind}
                onClick={() => {
                  set({ kind: option.kind });
                }}
                className={cn(
                  "hover:bg-accent focus-visible:ring-ring/50 rounded-md border px-3 py-2 text-left transition-colors outline-none focus-visible:ring-3",
                  form.kind === option.kind && "border-primary bg-muted/60 ring-primary/20 ring-2",
                )}
              >
                <span className="block text-sm font-medium">{option.name}</span>
                <span className="text-muted-foreground block text-xs leading-snug">
                  {option.blurb}
                </span>
              </button>
            ))}
          </div>
        </fieldset>
      )}

      <div className="space-y-1.5">
        <Label htmlFor="mcp-name">Name</Label>
        <Input
          id="mcp-name"
          value={form.name}
          autoComplete="off"
          placeholder={form.kind === "http" ? "github" : "filesystem"}
          maxLength={maxServerName}
          className="font-mono"
          required
          aria-invalid={form.name !== "" && problemFor("name") !== null}
          aria-describedby="mcp-name-problem"
          onChange={(e) => {
            set({ name: e.target.value });
          }}
        />
        <FieldProblem
          id="mcp-name-problem"
          message={problemFor("name")}
          shown={form.name !== ""}
          hint={`Tools reach the model as mcp__${form.name.trim() || "name"}__tool.`}
        />
      </div>

      {form.kind === "http" ? (
        <>
          <div className="space-y-1.5">
            <Label htmlFor="mcp-url">URL</Label>
            <Input
              id="mcp-url"
              value={form.url}
              autoComplete="off"
              type="url"
              required
              placeholder="https://mcp.example.com/mcp"
              className="font-mono"
              aria-invalid={form.url !== "" && problemFor("url") !== null}
              aria-describedby={problemFor("url") ? "mcp-url-problem" : undefined}
              onChange={(e) => {
                set({ url: e.target.value });
              }}
            />
            <FieldProblem
              id="mcp-url-problem"
              message={problemFor("url")}
              shown={form.url !== ""}
            />
            {moved && (
              <Notice className="mt-1">
                The URL changed, so saving signs the server out: its tokens were issued for the old
                URL.
                {dropped.length > 0 &&
                  ` The stored ${dropped.length === 1 ? "header" : "headers"} ${dropped.join(", ")} ${dropped.length === 1 ? "is" : "are"} not sent to the new URL unless you enter ${dropped.length === 1 ? "its value" : "their values"} again.`}
              </Notice>
            )}
          </div>
          <PairsEditor
            what="header"
            pairs={form.headers}
            onChange={(headers) => {
              set({ headers });
            }}
            problem={problemFor("headers")}
            hint="Sent with every request, such as an API key. An Authorization header replaces OAuth sign-in."
          />
          <Collapsible
            defaultOpen={form.clientId !== ""}
            className="overflow-hidden rounded-md border"
          >
            <CollapsibleTrigger className="group hover:bg-accent focus-visible:ring-ring flex w-full items-center gap-2 px-3 py-2 text-left text-sm font-medium transition-colors focus-visible:ring-1 focus-visible:outline-none">
              <ChevronRight
                aria-hidden
                className="size-4 shrink-0 transition-transform group-data-[state=open]:rotate-90"
              />
              OAuth client
            </CollapsibleTrigger>
            <CollapsibleContent className="space-y-3 border-t px-3 py-3">
              <p className="text-muted-foreground text-xs">
                Most servers need nothing here: Eika registers itself with their authorization
                server. Enter a client only if that server asks you to register one by hand.
              </p>
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label htmlFor="mcp-client-id">Client ID</Label>
                  <Input
                    id="mcp-client-id"
                    value={form.clientId}
                    autoComplete="off"
                    className="font-mono"
                    onChange={(e) => {
                      set({ clientId: e.target.value });
                    }}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="mcp-client-secret">Client secret</Label>
                  <Input
                    id="mcp-client-secret"
                    type="password"
                    value={form.clientSecret}
                    autoComplete="off"
                    className="font-mono"
                    disabled={form.clientId.trim() === ""}
                    placeholder={
                      server?.oauth_client_secret_set === true && !form.removeSecret
                        ? "stored; leave blank to keep it"
                        : "none, for a public client"
                    }
                    onChange={(e) => {
                      set({ clientSecret: e.target.value, removeSecret: false });
                    }}
                  />
                  {server?.oauth_client_secret_set === true &&
                    form.clientId.trim() !== "" &&
                    !form.removeSecret && (
                      <button
                        type="button"
                        className="text-muted-foreground hover:text-foreground text-xs underline underline-offset-4"
                        onClick={() => {
                          set({ removeSecret: true, clientSecret: "" });
                        }}
                      >
                        Remove the stored secret
                      </button>
                    )}
                </div>
              </div>
            </CollapsibleContent>
          </Collapsible>
        </>
      ) : (
        <>
          <div className="grid gap-4 sm:grid-cols-[1fr_2fr]">
            <div className="space-y-1.5">
              <Label htmlFor="mcp-command">Command</Label>
              <Input
                id="mcp-command"
                value={form.command}
                autoComplete="off"
                placeholder="npx"
                className="font-mono"
                required
                aria-invalid={form.command !== "" && problemFor("command") !== null}
                aria-describedby={problemFor("command") ? "mcp-command-problem" : undefined}
                onChange={(e) => {
                  set({ command: e.target.value });
                }}
              />
              <FieldProblem
                id="mcp-command-problem"
                message={problemFor("command")}
                shown={form.command !== ""}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="mcp-args">Arguments, one per line</Label>
              <Textarea
                id="mcp-args"
                value={form.args}
                rows={3}
                placeholder={"-y\n@modelcontextprotocol/server-filesystem\n."}
                className="font-mono text-xs"
                onChange={(e) => {
                  set({ args: e.target.value });
                }}
              />
            </div>
          </div>
          <PairsEditor
            what="variable"
            pairs={form.env}
            onChange={(env) => {
              set({ env });
            }}
            problem={problemFor("env")}
            hint="Added to the workspace's environment when the server starts."
          />
          <Notice>
            The server runs inside each workspace that uses it, with the workspace&apos;s files. Its
            variables are visible to the agent there, so do not give it a secret the agent should
            not read.
          </Notice>
        </>
      )}

      {saveError && (
        <ActionError
          action={editing ? `save ${server.name}` : "add the server"}
          error={saveError}
        />
      )}

      <div className="flex flex-wrap items-center justify-end gap-2">
        <Button type="button" variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" disabled={problem !== null || saving}>
          {saving ? "Saving…" : editing ? "Save" : "Add server"}
        </Button>
      </div>
    </form>
  );
}

type PairsEditorProps = {
  what: "header" | "variable";
  pairs: Pair[];
  onChange: (pairs: Pair[]) => void;
  problem: string | null;
  hint: string;
};

/** PairsEditor edits a server's headers or environment variables, a row each. */
function PairsEditor({ what, pairs, onChange, problem, hint }: PairsEditorProps) {
  const title = what === "header" ? "Headers" : "Environment variables";
  const id = `mcp-${what}s`;
  const change = (index: number, patch: Partial<Pair>) => {
    onChange(pairs.map((p, i) => (i === index ? { ...p, ...patch } : p)));
  };
  return (
    <fieldset className="space-y-2" aria-describedby={`${id}-hint`}>
      <legend className="text-sm font-medium">{title}</legend>
      {pairs.length > 0 && (
        <ul className="space-y-1.5">
          {pairs.map((pair, index) => (
            <li key={index} className="flex items-center gap-1.5">
              <Input
                aria-label={`${what === "header" ? "Header" : "Variable"} ${String(index + 1)} name`}
                value={pair.name}
                autoComplete="off"
                placeholder={what === "header" ? "X-Api-Key" : "LOG_LEVEL"}
                className="h-8 flex-1 font-mono text-xs"
                // A stored pair's value is sealed under its name, so renaming
                // it would lose the value; it is removed and added instead.
                readOnly={pair.stored}
                onChange={(e) => {
                  change(index, { name: e.target.value });
                }}
              />
              <Input
                aria-label={`${pair.name.trim() || `${what === "header" ? "Header" : "Variable"} ${String(index + 1)}`} value`}
                type="password"
                value={pair.value}
                autoComplete="off"
                placeholder={pair.stored ? "stored; leave blank to keep it" : "value"}
                className="h-8 flex-[2] font-mono text-xs"
                onChange={(e) => {
                  change(index, { value: e.target.value });
                }}
              />
              <Button
                type="button"
                size="icon-xs"
                variant="ghost"
                aria-label={`Remove ${pair.name.trim() || `${what} ${String(index + 1)}`}`}
                onClick={() => {
                  onChange(pairs.filter((_, i) => i !== index));
                }}
              >
                <Trash2 aria-hidden />
              </Button>
            </li>
          ))}
        </ul>
      )}
      {problem !== null && (
        <p className="text-destructive text-xs" role="alert">
          {problem}
        </p>
      )}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p id={`${id}-hint`} className="text-muted-foreground text-xs">
          {hint} Values are encrypted and never shown again.
        </p>
        <Button
          type="button"
          size="xs"
          variant="outline"
          onClick={() => {
            onChange([...pairs, { name: "", value: "", stored: false }]);
          }}
        >
          <Plus aria-hidden />
          Add {what}
        </Button>
      </div>
    </fieldset>
  );
}

type FieldProblemProps = {
  id: string;
  message: string | null;
  /** shown marks the problem as an error; otherwise it reads as a hint. */
  shown: boolean;
  /** hint is what the field says when nothing is wrong with it. */
  hint?: string;
};

/** FieldProblem is what keeps one field from being used, or its hint, right below it. */
function FieldProblem({ id, message, shown, hint }: FieldProblemProps) {
  const text = message ?? hint;
  if (text === undefined) return null;
  return (
    <p
      id={id}
      className={cn(
        "text-xs",
        message !== null && shown ? "text-destructive" : "text-muted-foreground",
      )}
    >
      {text}
    </p>
  );
}
