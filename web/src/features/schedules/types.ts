export type Routine = {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  schedule_kind: string;
  schedule_expr: string;
  timezone: string;
  schedule_words: string;
  next_five: string[];
  next_run_at: string | null;
  agent: string;
  prompt: string;
  approved_at: string | null;
  warning: { spans: unknown[] | null; unscanned: boolean; max_score: number };
  last_outcome: string;
  last_reason: string;
};
export type Wakeup = {
  id: string;
  session_id: string;
  agent: string;
  note: string;
  due_at: string;
  state: string;
};
