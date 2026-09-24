import { createFileRoute, Link, redirect } from "@tanstack/react-router";
import { backend_url } from "../../api";
import { consumeOAuthState } from "../../oauthState";

type oauthCode = {
  code: string;
  state: string;
  scope: string;
  // Set instead of code when the user declines, e.g. "access_denied".
  error: string;
};

export const Route = createFileRoute("/oauth/glink")({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>): oauthCode => {
    // validate and parse the search params into a typed state
    return {
      code: (search.code as string) || "",
      state: (search.state as string) || "",
      scope: (search.scope as string) || "",
      error: (search.error as string) || "",
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
      // Replace this history entry, so Back skips the spent callback URL.
      throw redirect({
        href: `${backend_url}/api/glink?${params}`,
        replace: true,
      });
    }
  },
});

function RouteComponent() {
  const { error } = Route.useSearch();
  let message =
    "Account linking failed: the response from Google doesn't match this browser session.";
  if (error === "access_denied") {
    message = "Linking was cancelled.";
  } else if (error) {
    message = `Account linking failed: Google returned "${error}".`;
  }
  return (
    <div>
      <p>{message}</p>
      <Link to="/request">Try again</Link>
    </div>
  );
}
