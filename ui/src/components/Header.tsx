import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useRouterState } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { getMe, logout } from "../api";
import { queryKeys } from "../api/queryKeys";
import { setSession } from "../api/session";
import Icon, { Logo } from "./ui/Icon";

// The app's top bar: name, nav tabs and the user menu; on phones the tabs
// move into a menu. See docs/archive/ui-refresh.md, "App shell".

/** The name and mark, linking to Browse. */
export function Brand() {
  return (
    <Link
      to="/"
      search={{}}
      className="flex items-center gap-2 text-lg font-semibold tracking-tight"
    >
      <Logo />
      Bhandaar
    </Link>
  );
}

const tabs = [
  { to: "/", label: "Browse" },
  { to: "/request", label: "Request" },
  { to: "/requests", label: "Request History" },
  { to: "/duplicates", label: "Duplicates" },
  { to: "/manage-data", label: "Manage data" },
] as const;

// Which tab a page belongs to: a scan's page is part of Request History.
function currentTab(pathname: string): string {
  if (pathname.startsWith("/scans/")) {
    return "/requests";
  }
  return tabs.find((t) => t.to === pathname)?.to ?? "";
}

function NavLinks({
  vertical = false,
  onNavigate,
}: {
  vertical?: boolean;
  onNavigate?: () => void;
}) {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const current = currentTab(pathname);
  return (
    <>
      {tabs.map((t) => {
        const active = t.to === current;
        return (
          <Link
            key={t.to}
            to={t.to}
            search={{}}
            onClick={onNavigate}
            aria-current={active ? "page" : undefined}
            className={
              vertical
                ? `block rounded-md px-3 py-2.5 ${active ? "bg-accent-soft font-medium text-accent" : "hover:bg-surface-muted"}`
                : `flex h-14 items-center border-b-2 px-1 text-sm transition-colors ${
                    active
                      ? "border-accent font-medium text-accent"
                      : "border-transparent text-muted hover:text-fg"
                  }`
            }
          >
            {t.label}
          </Link>
        );
      })}
    </>
  );
}

// Closes a popup on Escape and on a click outside ref.
function useDismiss(
  open: boolean,
  close: () => void,
  ref: React.RefObject<HTMLElement | null>
) {
  useEffect(() => {
    if (!open) {
      return;
    }
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && close();
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        close();
      }
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("mousedown", onClick);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("mousedown", onClick);
    };
  }, [open, close, ref]);
}

function UserMenu({ username }: { username: string }) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useDismiss(open, () => setOpen(false), ref);

  async function logOut() {
    try {
      await logout();
    } finally {
      setSession(queryClient, null);
      navigate({ to: "/login", search: { redirect: "/" } });
    }
  }

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`Signed in as ${username}`}
        onClick={() => setOpen(!open)}
        className="flex items-center gap-1.5 rounded-md px-2 py-1.5 text-sm hover:bg-surface-muted"
      >
        <Icon name="user" />
        <span className="max-w-32 truncate">{username}</span>
        <Icon name="chevronDown" className="text-muted" />
      </button>
      {open && (
        <div
          role="menu"
          className="absolute right-0 z-20 mt-1 w-48 rounded-lg border border-line bg-surface py-1 shadow-sm"
        >
          <p className="px-3 py-2 text-xs text-muted">
            Signed in as {username}
          </p>
          <button
            type="button"
            role="menuitem"
            onClick={logOut}
            className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-surface-muted"
          >
            <Icon name="logOut" />
            Log out
          </button>
        </div>
      )}
    </div>
  );
}

export default function Header() {
  // Only reads what the root route's session check loaded.
  const { data: me } = useQuery({
    queryKey: queryKeys.me,
    queryFn: getMe,
    enabled: false,
  });
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  useDismiss(menuOpen, () => setMenuOpen(false), menuRef);

  return (
    <header className="sticky top-0 z-10 border-b border-line bg-surface">
      <div
        ref={menuRef}
        className="mx-auto flex h-14 max-w-6xl items-center gap-6 px-4"
      >
        <button
          type="button"
          aria-label={menuOpen ? "Close menu" : "Menu"}
          aria-expanded={menuOpen}
          onClick={() => setMenuOpen(!menuOpen)}
          className="-ml-2 rounded-md p-2 hover:bg-surface-muted sm:hidden"
        >
          <Icon name={menuOpen ? "close" : "menu"} size={20} />
        </button>
        <Brand />
        <nav aria-label="Main" className="hidden gap-5 sm:flex">
          <NavLinks />
        </nav>
        <div className="ml-auto">
          {me && <UserMenu username={me.username} />}
        </div>
        {menuOpen && (
          <nav
            aria-label="Main"
            className="absolute inset-x-0 top-14 border-b border-line bg-surface p-2 shadow-sm sm:hidden"
          >
            <NavLinks vertical onNavigate={() => setMenuOpen(false)} />
          </nav>
        )}
      </div>
    </header>
  );
}
