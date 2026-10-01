import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { login } from "../api";
import { setSession } from "../api/session";
import Button from "../components/ui/Button";
import Card from "../components/ui/Card";
import Field from "../components/ui/Field";
import Icon from "../components/ui/Icon";
import Input from "../components/ui/Input";
import { safeRedirect } from "../safeRedirect";

type LoginSearch = { redirect: string };

export const Route = createFileRoute("/login")({
  component: Login,
  validateSearch: (search: Record<string, unknown>): LoginSearch => ({
    redirect: safeRedirect(String(search.redirect ?? "/")),
  }),
});

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
    <Card className="mx-auto max-w-sm">
      <form
        className="grid gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          mutate();
        }}
      >
        <div className="grid gap-1">
          <h1 className="text-xl font-semibold">Log in</h1>
          <p className="text-sm text-muted">
            Use your driveagent account (set up with{" "}
            <code className="rounded bg-surface-muted px-1 py-0.5 text-xs">
              agentserver user add
            </code>
            ).
          </p>
        </div>
        <Field label="Username">
          {(control) => (
            <Input
              {...control}
              name="username"
              autoComplete="username"
              required
              autoFocus
              value={username}
              onChange={(e) => setUsername(e.target.value)}
            />
          )}
        </Field>
        <Field label="Password">
          {(control) => (
            <Input
              {...control}
              name="password"
              type="password"
              autoComplete="current-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          )}
        </Field>
        {error && (
          <p
            role="alert"
            className="flex items-start gap-2 rounded-md bg-danger/10 px-3 py-2 text-sm text-danger"
          >
            <Icon name="warning" className="mt-0.5 shrink-0" />
            {error.message}
          </p>
        )}
        <Button type="submit" loading={isPending} className="w-full">
          {isPending ? "Logging in…" : "Log in"}
        </Button>
      </form>
      {/* What the app is, and its privacy policy and terms, for anyone who
          lands here; Google's OAuth verification checks the homepage for
          them. */}
      <p className="mt-6 border-t border-line pt-4 text-xs text-muted">
        Bhandaar shows what takes up space in your Google Drive, Gmail, Google
        Photos and Google Cloud Storage, and on your own drives, using read-only
        access. It&apos;s private and invite-only.{" "}
        <a href="/privacy.html" className="text-accent hover:underline">
          Privacy policy
        </a>{" "}
        ·{" "}
        <a
          href="/terms-of-service.html"
          className="text-accent hover:underline"
        >
          Terms of Service
        </a>
      </p>
    </Card>
  );
}
