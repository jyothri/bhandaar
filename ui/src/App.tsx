import {
  MutationCache,
  QueryCache,
  QueryClient,
  QueryClientProvider,
} from "@tanstack/react-query";
import { RouterProvider, createRouter } from "@tanstack/react-router";

import "./App.css";

import { UnauthenticatedError } from "./api";
import { setSession } from "./api/session";
// Import the generated route tree
import { routeTree } from "./routeTree.gen";

// A 401 means the session ended (expired, logged out elsewhere, or the user
// was disabled): forget who was logged in, and re-run the root route's
// session check, which sends the user to the login page.
function onError(error: Error) {
  if (error instanceof UnauthenticatedError) {
    setSession(queryClient, null);
    router.invalidate();
  }
}

const queryClient = new QueryClient({
  queryCache: new QueryCache({ onError }),
  mutationCache: new MutationCache({ onError }),
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
    },
  },
});

// Create a new router instance
const router = createRouter({ routeTree, context: { queryClient } });

// Register the router instance for type safety
declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

function App() {
  return (
    <>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </>
  );
}

export default App;
