import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { render } from "@testing-library/react";
import { vi } from "vitest";
import { routeTree } from "../routeTree.gen";

/** Renders the app's real route tree at `path`, with a fresh query cache. */
export function renderRoute(path: string) {
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  );
  return { router, queryClient };
}

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
