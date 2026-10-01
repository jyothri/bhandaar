import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { loggedIn, renderRoute, stubEventSource } from "./renderRoute";

// Logging in, through the real router with the backend faked. Nothing is
// logged in until POST /api/auth/login succeeds with alice's password.

const fetchMock = vi.fn<typeof fetch>();
let session = false;

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

beforeEach(() => {
  stubEventSource();
  session = false;
  fetchMock.mockReset();
  fetchMock.mockImplementation(async (input, init) => {
    const url = new URL(String(input));
    switch (url.pathname) {
      case "/api/auth/me":
        return session
          ? loggedIn()
          : json(401, {
              error: { code: "UNAUTHENTICATED", message: "log in first" },
            });
      case "/api/auth/login": {
        const { username, password } = JSON.parse(init?.body as string);
        if (username === "alice" && password === "correct horse battery") {
          session = true;
          return json(200, { username });
        }
        return json(401, {
          error: {
            code: "INVALID_CREDENTIALS",
            message: "invalid username or password",
          },
        });
      }
      case "/api/auth/logout":
        session = false;
        return new Response(null, { status: 204 });
      case "/api/scans/accounts":
        return json(200, []);
    }
    return json(404, {});
  });
  vi.stubGlobal("fetch", fetchMock);
});

async function logIn(password: string) {
  const user = userEvent.setup();
  await user.type(await screen.findByLabelText("Username"), "alice");
  await user.type(screen.getByLabelText("Password"), password);
  await user.click(screen.getByRole("button", { name: "Log in" }));
  return user;
}

describe("login", () => {
  it("sends a visitor without a session to the login page, and back after logging in", async () => {
    const { router } = renderRoute("/requests");

    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(router.state.location.search).toEqual({ redirect: "/requests" });

    await logIn("correct horse battery");

    await waitFor(() =>
      expect(router.state.location.pathname).toBe("/requests")
    );
    expect(
      await screen.findByRole("button", { name: "Signed in as alice" })
    ).toBeVisible();
    // The session cookie goes along with every request.
    for (const [, init] of fetchMock.mock.calls) {
      expect(init?.credentials).toBe("include");
    }
  });

  it("shows the backend's message for a wrong password", async () => {
    const { router } = renderRoute("/login");

    await logIn("wrong");

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "invalid username or password"
    );
    expect(router.state.location.pathname).toBe("/login");
  });

  it("says what the app is, and links to its privacy policy and terms", async () => {
    renderRoute("/login");

    expect(
      await screen.findByText(/Bhandaar shows what takes up space/)
    ).toBeVisible();
    expect(
      screen.getByRole("link", { name: "Privacy policy" })
    ).toHaveAttribute("href", "/privacy.html");
    expect(
      screen.getByRole("link", { name: "Terms of Service" })
    ).toHaveAttribute("href", "/terms-of-service.html");
  });

  it("never redirects off the site after logging in", async () => {
    const { router } = renderRoute(
      "/login?redirect=%2F%2Fevil.example.com%2Fx"
    );

    await logIn("correct horse battery");

    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
  });

  it("logs out", async () => {
    session = true;
    const { router } = renderRoute("/");
    const user = userEvent.setup();

    // Log out is in the user menu.
    await user.click(
      await screen.findByRole("button", { name: "Signed in as alice" })
    );
    await user.click(screen.getByRole("menuitem", { name: "Log out" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(session).toBe(false);
    expect(
      screen.queryByRole("button", { name: "Signed in as alice" })
    ).not.toBeInTheDocument();
  });
});

describe("app shell", () => {
  it("opens the phone menu, and closes it on Escape", async () => {
    session = true;
    renderRoute("/");
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Menu" }));
    expect(screen.getAllByRole("navigation", { name: "Main" })).toHaveLength(2);
    await user.keyboard("{Escape}");
    expect(screen.getAllByRole("navigation", { name: "Main" })).toHaveLength(1);
  });

  it("names the page in the browser tab", async () => {
    session = true;
    renderRoute("/requests");
    await waitFor(() =>
      expect(document.title).toBe("Request History · Bhandaar")
    );
  });
});
