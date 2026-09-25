/**
 * Which MCP server the settings show. It is a store rather than state of the
 * MCP tab because the OAuth callback page, which the browser returns to after
 * signing in, opens the settings on the server it authorized.
 */

import { create } from "zustand";

type MCPSelection = {
  /** serverId is the server whose page is open, empty for the list. */
  serverId: string;
  select: (serverId: string) => void;
};

/** useMCPSelection is the server the MCP settings tab shows. */
export const useMCPSelection = create<MCPSelection>((set) => ({
  serverId: "",
  select: (serverId) => {
    set({ serverId });
  },
}));
