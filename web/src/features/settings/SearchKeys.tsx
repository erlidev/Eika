/** The search API keys, each saved or removed on its own and never shown again. */

import { useState } from "react";

import type { SearchStatus } from "@/api/types";
import { ActionError } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { keyLabels, useSaveSearchKey } from "@/features/search";

type SearchKeysProps = { status: SearchStatus };

export function SearchKeys({ status }: SearchKeysProps) {
  return (
    <div className="space-y-3">
      <div>
        <h3 className="text-sm font-medium">API keys</h3>
        <p className="text-muted-foreground text-xs">
          Sealed in the harness and never shown again. Exa, Tavily, and Brave are used only when
          they have a key. A GitHub token enables code search and raises GitHub&apos;s rate limit
          for the other sources and for reading GitHub pages.
        </p>
      </div>
      {status.keys.map((key) => (
        <KeyForm key={key.name} name={key.name} set={key.set} hint={key.hint} />
      ))}
    </div>
  );
}

/** keyName names a search key in a sentence: "Exa key", "GitHub token". */
function keyName(name: string): string {
  const label = keyLabels[name] ?? name;
  return name === "github" ? label : `${label} key`;
}

type KeyFormProps = { name: string; set: boolean; hint?: string | undefined };

function KeyForm({ name, set, hint }: KeyFormProps) {
  const save = useSaveSearchKey();
  const [key, setKey] = useState("");
  const id = `search-key-${name}`;
  return (
    <form
      className="space-y-1"
      onSubmit={(e) => {
        e.preventDefault();
        if (key.trim() === "") return;
        save.mutate(
          { name, key: key.trim() },
          {
            onSuccess: () => {
              setKey("");
            },
          },
        );
      }}
    >
      <Label htmlFor={id} className="text-xs">
        {keyLabels[name] ?? name}
      </Label>
      <div className="flex gap-2">
        <Input
          id={id}
          type="password"
          autoComplete="off"
          value={key}
          placeholder={
            set ? `Stored${hint ? `, ends in ${hint}` : ""}. Type to replace.` : "Not set"
          }
          onChange={(e) => {
            setKey(e.target.value);
          }}
        />
        <Button type="submit" variant="outline" disabled={key.trim() === "" || save.isPending}>
          Save
        </Button>
        <Button
          type="button"
          variant="ghost"
          disabled={!set || save.isPending}
          onClick={() => {
            save.mutate({ name, key: "" });
          }}
        >
          Remove
        </Button>
      </div>
      {save.isError && <ActionError action={`update the ${keyName(name)}`} error={save.error} />}
    </form>
  );
}
