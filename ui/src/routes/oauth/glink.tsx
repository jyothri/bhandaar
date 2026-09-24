import { createFileRoute, Link, redirect } from "@tanstack/react-router";
import { backend_url } from "../../api";
import { consumeOAuthState } from "../../oauthState";

type oauthCode = {
  code: string;
  state: string;
  scope: string;
};

export const Route = createFileRoute("/oauth/glink")({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>): oauthCode => {
    // validate and parse the search params into a typed state
    return {
      code: (search.code as string) || "",
      state: (search.state as string) || "",
      scope: (search.scope as string) || "",
    };
  },
  // Hand the one-time code to the backend before anything renders, so it is
  // sent exactly once. The component only renders if the check fails.
  beforeLoad: ({ search: { code, state, scope } }) => {
    if (code && consumeOAuthState(state)) {
      const params = new URLSearchParams({
        code,
        redirectUri: `${window.location.origin}/oauth/glink`,
        state,
        scope,
      });
      throw redirect({ href: `${backend_url}/api/glink?${params}` });
    }
  },
});

function RouteComponent() {
  return (
    <div>
      <p>
        Account linking failed: the response from Google doesn't match this
        browser session.
      </p>
      <Link to="/request">Try again</Link>
    </div>
  );
}
