import { QueryClient } from "@tanstack/react-query";
import {
  createRootRouteWithContext,
  Link,
  Outlet,
  redirect,
  useRouterState,
} from "@tanstack/react-router";
import { TanStackRouterDevtools } from "@tanstack/react-router-devtools";
import { getMe } from "../api";
import { queryKeys } from "../api/queryKeys";
import Header from "../components/Header";

export type RouterContext = { queryClient: QueryClient };

// Pages that work without a session. The OAuth callback hands its one-time
// code straight to the backend, which checks the session itself.
const publicPaths = ["/login", "/oauth/glink"];

export const Route = createRootRouteWithContext<RouterContext>()({
  // Every other page needs a logged-in user. The answer is cached; a 401
  // later on (see App) clears it and comes back here.
  beforeLoad: async ({ context: { queryClient }, location }) => {
    if (publicPaths.includes(location.pathname)) {
      return;
    }
    const me = await queryClient.ensureQueryData({
      queryKey: queryKeys.me,
      queryFn: getMe,
    });
    if (!me) {
      throw redirect({ to: "/login", search: { redirect: location.href } });
    }
  },
  component: RootLayout,
});

function RootLayout() {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  return (
    <>
      <Header />
      <div className="p-2 flex gap-2">
        <Link
          to="/"
          search={{}}
          activeOptions={{ exact: true, includeSearch: false }}
          className="[&.active]:font-bold"
        >
          Browse
        </Link>
        <Link to="/request" className="[&.active]:font-bold">
          Request
        </Link>
        <Link
          to="/requests"
          // A scan's page is part of Request History.
          className={
            pathname.startsWith("/scans/") ? "font-bold" : "[&.active]:font-bold"
          }
        >
          Request History
        </Link>
      </div>
      <hr />
      <Outlet />
      <TanStackRouterDevtools />
    </>
  );
}
