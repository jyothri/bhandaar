import { createFileRoute } from "@tanstack/react-router";
import { backend_url } from "../../api";

type oauthCode = {
  code: string;
};

export const Route = createFileRoute("/oauth/glink")({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>): oauthCode => {
    // validate and parse the search params into a typed state
    return {
      code: (search.code as string) || "",
    };
  },
});

function RouteComponent() {
  const { code } = Route.useSearch();
  const redirectUri = `${window.location.protocol}//${window.location.host}/oauth/glink`;
  const url = `${backend_url}/oauth/glink?code=${code}&redirectUri=${redirectUri}`;
  window.location.href = url;
  return (
    <div>
      <p>Processing...</p>
    </div>
  );
}
