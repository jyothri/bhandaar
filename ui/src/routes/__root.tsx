import { QueryClient } from "@tanstack/react-query";
import {
  createRootRouteWithContext,
  Outlet,
  redirect,
  useRouterState,
} from "@tanstack/react-router";
import { useEffect } from "react";
import { TanStackRouterDevtools } from "@tanstack/react-router-devtools";
import { getMe } from "../api";
import { queryKeys } from "../api/queryKeys";
import Header, { Brand } from "../components/Header";
import { settingsQuery } from "../components/hooks/useSettings";

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
    // The settings decide which tabs show; loaded once with the session, so
    // the tabs don't shift on first paint. A failure leaves the pages to
    // retry.
    await queryClient.ensureQueryData(settingsQuery).catch(() => undefined);
  },
  component: RootLayout,
});

// The page's name in the browser tab: "<page> · Bhandaar".
const pageTitles: Record<string, string> = {
  "/": "Browse",
  "/request": "Request",
  "/requests": "Request History",
  "/duplicates": "Duplicates",
  "/manage-data": "Manage data",
  "/settings": "Settings",
  "/login": "Log in",
};

function pageTitle(pathname: string): string {
  const scan = pathname.match(/^\/scans\/([^/]+)/);
  const page = pageTitles[pathname] ?? (scan ? `Scan ${scan[1]}` : "");
  return page ? `${page} · Bhandaar` : "Bhandaar";
}

function RootLayout() {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  useEffect(() => {
    document.title = pageTitle(pathname);
  }, [pathname]);

  if (publicPaths.includes(pathname)) {
    // Login and the OAuth callback: just the name, above their content.
    return (
      <main className="mx-auto max-w-6xl px-4 py-10">
        <div className="mb-6 flex justify-center">
          <Brand />
        </div>
        <Outlet />
      </main>
    );
  }
  return (
    <>
      <Header />
      <main className="mx-auto max-w-6xl px-4 pb-10">
        <Outlet />
      </main>
      <TanStackRouterDevtools />
    </>
  );
}
