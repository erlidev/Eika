/**
 * The notice for a session whose configuration did not load. The configuration
 * names the model the session resolves to, so without it the composer cannot
 * tell whether images are accepted; the notice says what failed, with Retry.
 */

import { LoadError } from "@/components/Notice";

export type ConfigurationErrorProps = {
  error: Error;
  retry: () => void;
  retrying: boolean;
};

export function ConfigurationError({ error, retry, retrying }: ConfigurationErrorProps) {
  return (
    <div className="border-t px-3 py-2">
      <LoadError
        what="the session's model"
        error={error}
        retry={retry}
        retrying={retrying}
        className="mx-auto max-w-3xl"
      />
    </div>
  );
}
