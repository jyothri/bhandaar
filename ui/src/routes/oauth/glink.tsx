import { createFileRoute, Link } from "@tanstack/react-router";
import { backend_url } from "../../api";
import { isExpectedOAuthState } from "../../oauthState";

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
});

function RouteComponent() {
  const { code, state, scope } = Route.useSearch();
  if (!isExpectedOAuthState(state)) {
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
  const params = new URLSearchParams({
    code,
    redirectUri: `${window.location.origin}/oauth/glink`,
    state,
    scope,
  });
  window.location.href = `${backend_url}/api/glink?${params}`;
  return (
    <div>
      <p>Processing...</p>
    </div>
  );
}
