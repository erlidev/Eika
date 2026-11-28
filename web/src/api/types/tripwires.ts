/** Tripwire entries in the Inbox: the wire types of `GET /api/inbox/tripwires` and its answers. */

/** TripwireName is the chain that fired. */
export type TripwireName = "followed" | "carried_out" | "lifted" | "drained" | "blocked_often";

/** TripwireStatus is open until the user answers "this was fine" or "stop the run". */
export type TripwireStatus = "open" | "fine" | "stopped";

/** TripwireSource is where the destination first appeared in the session. */
export type TripwireSource = "user" | "search" | "content" | "model";

/** TripwireEntry is one Inbox entry a tripwire made. */
export type TripwireEntry = {
  id: string;
  at: string;
  tripwire: TripwireName;
  held: boolean;
  host?: string;
  source?: TripwireSource;
  summary: string;
  session_id: string;
  run_id: string;
  running: boolean;
  status: TripwireStatus;
  answered_at?: string;
};

/** TripwireEntries is the open entries, newest first. */
export type TripwireEntries = {
  entries: TripwireEntry[];
};
