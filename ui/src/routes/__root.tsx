import { createRootRoute, Link, Outlet } from "@tanstack/react-router";
import { TanStackRouterDevtools } from "@tanstack/react-router-devtools";
import Header from "../components/Header";

export const Route = createRootRoute({
  component: () => (
    <>
      <Header />
      <div className="p-2 flex gap-2">
        <Link to="/" className="[&.active]:font-bold">
          Data
        </Link>
        <Link to="/request" className="[&.active]:font-bold">
          Request
        </Link>
        <Link to="/requests" className="[&.active]:font-bold">
          Request History
        </Link>
      </div>
      <hr />
      <Outlet />
      <TanStackRouterDevtools />
    </>
  ),
});
