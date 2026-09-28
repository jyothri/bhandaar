import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { getMe, logout } from "../api";
import { queryKeys } from "../api/queryKeys";
import { setSession } from "../api/session";

export default function Header() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  // Only reads what the root route's session check loaded; public pages
  // (login, the OAuth callback) don't ask the backend.
  const { data: me } = useQuery({
    queryKey: queryKeys.me,
    queryFn: getMe,
    enabled: false,
  });

  async function logOut() {
    try {
      await logout();
    } finally {
      setSession(queryClient, null);
      navigate({ to: "/login", search: { redirect: "/" } });
    }
  }

  return (
    <div className="bg-white dark:bg-gray-900">
      <h1 className="text-3xl font-bold flex justify-center items-center p-8 dark:text-white">
        Storage Manager Web App
      </h1>
      {me && (
        <div className="flex justify-end items-center gap-2 px-4 dark:text-white">
          <span>Signed in as {me.username}</span>
          <button type="button" className="underline" onClick={logOut}>
            Log out
          </button>
        </div>
      )}
    </div>
  );
}
