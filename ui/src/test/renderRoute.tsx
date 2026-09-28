import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { render } from "@testing-library/react";
import { vi } from "vitest";
import { routeTree } from "../routeTree.gen";

/**
 * Renders the app's real route tree at `path`, with a fresh query cache.
 * Pages other than /login and /oauth/glink first ask the backend
 * (GET /api/auth/me) who is logged in; tests fake it, e.g. with loggedIn.
 */
export function renderRoute(path: string) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const router = createRouter({
    routeTree,
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  );
  return { router, queryClient };
}

/** A backend reply to GET /api/auth/me for a logged-in user. */
export const loggedIn = (username = "alice") =>
  new Response(JSON.stringify({ username }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

/** jsdom has no EventSource; the progress stream just never connects. */
export function stubEventSource() {
  vi.stubGlobal(
    "EventSource",
    class {
      static readonly CLOSED = 2;
      readyState = 0;
      onopen: (() => void) | null = null;
      onerror: (() => void) | null = null;
      addEventListener() {}
      close() {
        this.readyState = 2;
      }
    }
  );
}
