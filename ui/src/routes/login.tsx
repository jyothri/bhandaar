import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { login } from "../api";
import { setSession } from "../api/session";
import { safeRedirect } from "../safeRedirect";

type LoginSearch = { redirect: string };

export const Route = createFileRoute("/login")({
  component: Login,
  validateSearch: (search: Record<string, unknown>): LoginSearch => ({
    redirect: safeRedirect(String(search.redirect ?? "/")),
  }),
});

const inputClass =
  "p-2 border border-gray-300 rounded-sm dark:bg-gray-800 dark:border-gray-600 dark:text-white";

function Login() {
  const { redirect } = Route.useSearch();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");

  const { mutate, isPending, error } = useMutation({
    mutationFn: () => login(username, password),
    onSuccess: (user) => {
      setSession(queryClient, user);
      navigate({ href: redirect, replace: true });
    },
  });

  return (
    <form
      className="flex flex-col gap-3 max-w-sm mx-auto p-4"
      onSubmit={(e) => {
        e.preventDefault();
        mutate();
      }}
    >
      <h2 className="font-bold text-xl">Log in</h2>
      <p className="text-sm">
        Use your driveagent account (set up with{" "}
        <code>agentserver user add</code>).
      </p>
      <label className="flex flex-col gap-1">
        Username
        <input
          className={inputClass}
          name="username"
          autoComplete="username"
          required
          value={username}
          onChange={(e) => setUsername(e.target.value)}
        />
      </label>
      <label className="flex flex-col gap-1">
        Password
        <input
          className={inputClass}
          name="password"
          type="password"
          autoComplete="current-password"
          required
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </label>
      {error && (
        <p role="alert" className="text-red-600">
          {error.message}
        </p>
      )}
      <button
        type="submit"
        disabled={isPending}
        className="p-2 bg-blue-700 text-white rounded-sm disabled:opacity-50"
      >
        {isPending ? "Logging in…" : "Log in"}
      </button>
    </form>
  );
}
