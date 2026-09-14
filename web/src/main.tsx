import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";

import { App } from "@/app/App";
import { startTheme } from "@/app/theme";
import "./index.css";

const root = document.getElementById("root");
if (!root) {
  throw new Error("mount app: #root is missing from index.html");
}

/**
 * The event stream is what keeps server state current, so a query is not
 * refetched on a timer or on focus. A failed request is reported rather than
 * retried: the harness answers with a reason the user needs to see.
 */
const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: false, refetchOnWindowFocus: false, staleTime: 5000 },
  },
});

startTheme();

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
);
