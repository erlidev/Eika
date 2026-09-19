/** The sign-in password, signing out, and where this browser is connected. */

import { useMutation } from "@tanstack/react-query";
import { useState } from "react";

import { connect, disconnect } from "@/api/connection";
import { changePassword, signOut } from "@/api/routes";
import { Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
import { useConnection } from "@/features/connect";

/** minPasswordLength matches what the harness accepts. */
const minPasswordLength = 8;

export function AccountSettings() {
  const connection = useConnection();
  return (
    <div className="space-y-6">
      <PasswordForm baseUrl={connection.baseUrl} />
      <Separator />
      <div className="space-y-2">
        <h3 className="text-sm font-medium">This browser</h3>
        <dl className="text-muted-foreground grid grid-cols-[auto_1fr] gap-x-3 font-mono text-xs">
          <dt>harness</dt>
          <dd className="truncate">{connection.baseUrl || window.location.origin}</dd>
        </dl>
        <Button
          variant="secondary"
          size="sm"
          onClick={() => {
            // Ending the session on the harness is best effort: the browser
            // forgets its token either way.
            void signOut()
              .catch(() => undefined)
              .finally(disconnect);
          }}
        >
          Sign out
        </Button>
      </div>
    </div>
  );
}

function PasswordForm({ baseUrl }: { baseUrl: string }) {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [again, setAgain] = useState("");
  const change = useMutation({
    mutationFn: () => changePassword(current, next),
    onSuccess: (session) => {
      // Every other session ended; this browser carries on with a new token.
      connect({ baseUrl, token: session.token });
      setCurrent("");
      setNext("");
      setAgain("");
    },
  });

  const problem =
    current === ""
      ? "Enter the current password."
      : Array.from(next).length < minPasswordLength
        ? `The new password needs at least ${String(minPasswordLength)} characters.`
        : next !== again
          ? "The new passwords do not match."
          : null;

  return (
    <form
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (problem === null) change.mutate();
      }}
    >
      <div>
        <h3 className="text-sm font-medium">Password</h3>
        <p className="text-muted-foreground text-xs">Changing it signs out every other browser.</p>
      </div>
      <div className="grid gap-3 sm:grid-cols-3">
        <div className="space-y-1">
          <Label htmlFor="password-current" className="text-xs">
            Current password
          </Label>
          <Input
            id="password-current"
            type="password"
            autoComplete="current-password"
            value={current}
            onChange={(e) => {
              setCurrent(e.target.value);
            }}
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="password-new" className="text-xs">
            New password
          </Label>
          <Input
            id="password-new"
            type="password"
            autoComplete="new-password"
            value={next}
            onChange={(e) => {
              setNext(e.target.value);
            }}
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="password-again" className="text-xs">
            New password again
          </Label>
          <Input
            id="password-again"
            type="password"
            autoComplete="new-password"
            value={again}
            onChange={(e) => {
              setAgain(e.target.value);
            }}
          />
        </div>
      </div>
      {problem !== null && (current !== "" || next !== "") && (
        <p className="text-muted-foreground text-xs">{problem}</p>
      )}
      {change.isSuccess && <Notice tone="success">The password was changed.</Notice>}
      {change.isError && <Notice tone="error">{change.error.message}</Notice>}
      <Button
        type="submit"
        variant="outline"
        size="sm"
        disabled={problem !== null || change.isPending}
      >
        {change.isPending ? "Changing…" : "Change password"}
      </Button>
    </form>
  );
}
