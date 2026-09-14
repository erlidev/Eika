/** React access to the stored connection, without an effect. */

import { useSyncExternalStore } from "react";

import { getConnection, subscribeConnection } from "@/api/connection";
import type { Connection } from "@/api/connection";

const disconnected: Connection = { baseUrl: "", token: "" };

/** useConnection returns the deployment the UI is pointed at. */
export function useConnection(): Connection {
  return useSyncExternalStore(subscribeConnection, getConnection, () => disconnected);
}
