/** Halt is whether Eika is halted; at and reason are set only while it is. */
export type Halt = {
  halted: boolean;
  /** at is when it was halted, RFC 3339. */
  at?: string;
  reason?: string;
};

/** HaltRequest is the body of POST /api/halt. */
export type HaltRequest = {
  /** reason is a note of at most 200 bytes. */
  reason?: string;
};
