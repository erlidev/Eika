/** Live notifications derived from session changes and the home stream. */

import { useEffect, useRef, useState } from "react";
import { Link, useLocation } from "react-router";
import { X } from "lucide-react";

import { globalTopic, payloadOf, sessionTopic } from "@/api/events";
import { getSession } from "@/api/routes";
import { eventStream } from "@/api/stream";
import { useStreamSubscription } from "@/api/useStream";
import { useHome } from "@/features/chat";

type Toast = { id: string; text: string; path: string };

export function Toasts() {
  const home = useHome();
  const location = useLocation();
  const path = useRef(location.pathname);
  const [queue, setQueue] = useState<Toast[]>([]);
  const generation = useRef(0);
  const turns = useRef(new Map<string, { background: boolean; text: boolean }>());
  const seen = useRef(new Set<string>());
  const pending = useRef(new Map<string, string>());

  function add(toast: Toast) {
    if (seen.current.has(toast.id)) return;
    seen.current.add(toast.id);
    if (path.current === toast.path || (toast.id.startsWith("report:") && path.current === "/"))
      return;
    setQueue((items) => [...items, toast]);
  }

  useEffect(() => {
    const refresh = () => {
      generation.current++;
      turns.current.clear();
      pending.current.clear();
    };
    const stream = eventStream();
    const off = stream.onReopen(refresh);
    return off;
  }, []);

  useStreamSubscription(globalTopic, (event) => {
    if (event.type === "bus.dropped") {
      generation.current++;
      turns.current.clear();
      pending.current.clear();
      return;
    }
    const update = payloadOf(event, "session.updated");
    if (!update?.report_id) return;
    const id = `report:${update.report_id}`;
    const target = `/activity/${encodeURIComponent(update.session_id)}`;
    if (seen.current.has(id)) return;
    seen.current.add(id);
    if (path.current === target || path.current === "/") return;
    pending.current.set(id, target);
    const version = generation.current;
    const publish = (name: string) => {
      if (version !== generation.current || !pending.current.delete(id)) return;
      setQueue((items) => [...items, { id, text: `${name} reported`, path: target }]);
    };
    void getSession(update.session_id)
      .then(({ session }) => {
        publish(session.name ?? session.title);
      })
      .catch(() => {
        publish("Subagent");
      });
  });

  useStreamSubscription(home.data ? sessionTopic(home.data.session.id) : undefined, (event) => {
    const start = payloadOf(event, "turn.start");
    if (start)
      turns.current.set(start.run_id, {
        background: start.message === "" && !start.images?.length,
        text: false,
      });
    const delta = payloadOf(event, "message.delta");
    if (delta?.text.trim()) {
      const turn = turns.current.get(delta.run_id);
      if (turn) turn.text = true;
    }
    const reset = payloadOf(event, "message.reset");
    if (reset) {
      const turn = turns.current.get(reset.run_id);
      if (turn) turn.text = false;
    }
    const end = payloadOf(event, "turn.end");
    if (end) {
      const turn = turns.current.get(end.run_id);
      if (turn?.background && turn.text && end.stop_reason !== "aborted") {
        add({ id: `reply:${end.run_id}`, text: "Eika replied", path: "/" });
      }
      turns.current.delete(end.run_id);
    }
    const error = payloadOf(event, "run.error");
    if (error) turns.current.delete(error.run_id);
  });

  useEffect(() => {
    path.current = location.pathname;
    for (const [id, target] of pending.current) {
      if (target === location.pathname || location.pathname === "/") pending.current.delete(id);
    }
    // A queued notification is no longer news once its page is open.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setQueue((items) =>
      items.filter(
        (item) =>
          item.path !== location.pathname &&
          !(item.id.startsWith("report:") && location.pathname === "/"),
      ),
    );
  }, [location.pathname]);

  const toast = queue.find(
    (item) =>
      item.path !== location.pathname &&
      !(item.id.startsWith("report:") && location.pathname === "/"),
  );
  return (
    <div
      aria-live="polite"
      aria-atomic="true"
      className="fixed inset-x-3 top-0 pt-safe z-50 sm:inset-x-auto sm:top-auto sm:right-4 sm:bottom-4"
    >
      {toast && (
        <ToastCard
          key={toast.id}
          toast={toast}
          dismiss={() => {
            setQueue((items) => items.filter((item) => item.id !== toast.id));
          }}
        />
      )}
    </div>
  );
}

type ToastCardProps = { toast: Toast; dismiss: () => void };

function ToastCard({ toast, dismiss }: ToastCardProps) {
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  const latestDismiss = useRef(dismiss);
  useEffect(() => {
    latestDismiss.current = dismiss;
  }, [dismiss]);
  useEffect(() => {
    if (hovered || focused) return;
    const timer = setTimeout(() => {
      latestDismiss.current();
    }, 5000);
    return () => {
      clearTimeout(timer);
    };
  }, [hovered, focused]);

  return (
    <div
      className="bg-popover text-popover-foreground mx-auto mt-3 flex max-w-sm items-center gap-3 rounded-md border p-3 sm:mt-0"
      onMouseEnter={() => {
        setHovered(true);
      }}
      onMouseLeave={() => {
        setHovered(false);
      }}
      onFocus={() => {
        setFocused(true);
      }}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) setFocused(false);
      }}
    >
      <Link className="text-primary flex-1 text-sm underline" to={toast.path} onClick={dismiss}>
        {toast.text}
      </Link>
      <button
        type="button"
        aria-label="Dismiss notification"
        className="rounded-md p-1"
        onClick={dismiss}
      >
        <X className="size-4" />
      </button>
    </div>
  );
}
