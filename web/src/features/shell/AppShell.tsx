// The authenticated frame: sidebar (md and up) with the project switcher, a
// top bar, the page, a bottom tab bar on phones, and the app-wide overlays
// (task drawer, command palette, new-task / new-run dialogs).

import clsx from "clsx";
import { Ellipsis, LogOut, Monitor, Moon, Plus, Search, Settings, Sun } from "lucide-react";
import { useEffect, useState } from "react";
import { Link, NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";
import { useLogout, useProjects, useWaitingRuns } from "@/api/hooks";
import { ColorDot, Kbd } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { Menu } from "@/components/ui/Menu";
import { Skeleton } from "@/components/ui/Skeleton";
import { ProjectDialog } from "@/features/projects/ProjectDialog";
import { TaskDrawer } from "@/features/tasks/TaskDrawer";
import { useUser } from "@/lib/auth";
import { useTheme, type ThemePref } from "@/lib/theme";
import { CommandPalette } from "./CommandPalette";
import { ShellProvider, useShell } from "./context";
import { NAV } from "./nav";

export function AppShell() {
  return (
    <ShellProvider>
      <ShellFrame />
    </ShellProvider>
  );
}

function isTyping(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null;
  return !!el && (el.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(el.tagName));
}

function ShellFrame() {
  const { setPaletteOpen, newTask } = useShell();
  const location = useLocation();

  // Global shortcuts: ⌘/Ctrl+K opens the palette anywhere; "c" creates a task
  // when focus is not in a field.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPaletteOpen(true);
      } else if (e.key === "c" && !e.metaKey && !e.ctrlKey && !e.altKey && !isTyping(e.target) && !document.querySelector("[aria-modal=true]")) {
        e.preventDefault();
        const key = /^\/p\/([^/]+)/.exec(location.pathname)?.[1];
        newTask({ project_key: key });
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [setPaletteOpen, newTask, location.pathname]);

  return (
    <div className="flex min-h-dvh">
      <Sidebar />
      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar />
        <main className="min-w-0 flex-1 px-4 pt-4 pb-24 sm:px-6 md:pb-10">
          <Outlet />
        </main>
      </div>
      <MobileNav />
      <TaskDrawer />
      <CommandPalette />
    </div>
  );
}

function Logo() {
  return (
    <Link to="/" className="flex items-center gap-2 font-semibold tracking-tight">
      <img src="/favicon.svg" alt="" className="size-6" />
      <span>Forge</span>
    </Link>
  );
}

function WaitingBadge({ count, compact }: { count: number; compact?: boolean }) {
  if (!count) return null;
  const label = `${count} run${count === 1 ? "" : "s"} waiting for you`;
  return compact ? (
    <span className="absolute -top-0.5 -right-1 size-2 rounded-full bg-warning" role="status" aria-label={label} />
  ) : (
    <span className="ml-auto rounded-full bg-warning/20 px-1.5 text-[11px] font-semibold text-fg tabular" role="status" aria-label={label}>
      {count}
    </span>
  );
}

function Sidebar() {
  const projects = useProjects();
  const waiting = useWaitingRuns();
  const [creating, setCreating] = useState(false);
  return (
    <aside className="sticky top-0 hidden h-dvh w-60 shrink-0 flex-col border-r border-line bg-surface/60 md:flex">
      <div className="flex h-13 items-center px-4">
        <Logo />
      </div>
      <nav aria-label="Main" className="space-y-0.5 px-2">
        {NAV.map(({ to, label, icon: Icon, end }) => (
          <NavLink
            key={to}
            to={to}
            end={end}
            className={({ isActive }) =>
              clsx(
                "flex h-8 items-center gap-2.5 rounded-md px-2.5 text-[13px] transition-colors",
                isActive ? "bg-surface-3 font-medium text-fg" : "text-fg-2 hover:bg-surface-2 hover:text-fg",
              )
            }
          >
            <Icon className="size-4 text-fg-3" aria-hidden />
            {label}
            {to === "/agents" ? <WaitingBadge count={waiting} /> : null}
          </NavLink>
        ))}
      </nav>
      <div className="mt-5 flex items-center justify-between px-4 pb-1">
        <h2 className="text-[11px] font-semibold tracking-wide text-fg-3 uppercase">Projects</h2>
        <button
          type="button"
          onClick={() => setCreating(true)}
          className="rounded p-0.5 text-fg-3 hover:bg-surface-2 hover:text-fg"
          aria-label="New project"
        >
          <Plus className="size-3.5" />
        </button>
      </div>
      <nav aria-label="Projects" className="min-h-0 flex-1 space-y-0.5 overflow-y-auto px-2 pb-3">
        {projects.isPending
          ? Array.from({ length: 5 }, (_, i) => <Skeleton key={i} className="mx-1 h-7" />)
          : projects.data?.map((p) => {
              const open = p.stats.total - p.stats.done;
              return (
                <NavLink
                  key={p.key}
                  to={`/p/${p.key}`}
                  className={({ isActive }) =>
                    clsx(
                      "flex h-7.5 items-center gap-2.5 rounded-md px-2.5 text-[13px] transition-colors",
                      isActive ? "bg-surface-3 font-medium text-fg" : "text-fg-2 hover:bg-surface-2 hover:text-fg",
                    )
                  }
                >
                  <ColorDot color={p.color} />
                  <span className="min-w-0 flex-1 truncate">{p.name}</span>
                  {p.stats.endpoints_down > 0 ? (
                    <span className="size-1.5 rounded-full bg-critical" title={`${p.stats.endpoints_down} endpoint(s) down`} />
                  ) : null}
                  {open > 0 ? <span className="tabular text-[11px] text-fg-3">{open}</span> : null}
                </NavLink>
              );
            })}
      </nav>
      <UserMenu />
      {creating ? <ProjectDialog onClose={() => setCreating(false)} /> : null}
    </aside>
  );
}

function UserMenu() {
  const user = useUser();
  const logout = useLogout();
  const navigate = useNavigate();
  return (
    <div className="border-t border-line p-2">
      <Menu
        label="Account"
        align="start"
        className="w-full"
        triggerClassName="flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-left hover:bg-surface-2"
        trigger={
          <>
            <span className="grid size-6 place-items-center rounded-full bg-accent text-[11px] font-semibold text-accent-fg">
              {(user.display_name || user.username).slice(0, 1).toUpperCase()}
            </span>
            <span className="min-w-0 flex-1">
              <span className="block truncate text-[13px] font-medium">{user.display_name || user.username}</span>
              <span className="block truncate text-[11px] text-fg-3">{user.email}</span>
            </span>
          </>
        }
        items={[
          { label: "Settings", icon: <Settings />, onSelect: () => navigate("/settings") },
          {
            label: "Sign out",
            icon: <LogOut />,
            onSelect: () => logout.mutate(undefined, { onSettled: () => navigate("/login") }),
          },
        ]}
      />
    </div>
  );
}

export function ThemeToggle() {
  const { pref, setPref } = useTheme();
  const icon = pref === "light" ? <Sun className="size-4" /> : pref === "dark" ? <Moon className="size-4" /> : <Monitor className="size-4" />;
  const options: { value: ThemePref; label: string; icon: React.ReactNode }[] = [
    { value: "system", label: "System", icon: <Monitor /> },
    { value: "light", label: "Light", icon: <Sun /> },
    { value: "dark", label: "Dark", icon: <Moon /> },
  ];
  return (
    <Menu
      label={`Theme: ${pref}`}
      triggerClassName="grid size-8.5 place-items-center rounded-md text-fg-2 hover:bg-surface-2 hover:text-fg"
      trigger={icon}
      items={options.map((o) => ({ label: o.label, icon: o.icon, checked: pref === o.value, onSelect: () => setPref(o.value) }))}
    />
  );
}

function TopBar() {
  const { setPaletteOpen, newTask } = useShell();
  const location = useLocation();
  const navigate = useNavigate();
  const logout = useLogout();
  const projectKey = /^\/p\/([^/]+)/.exec(location.pathname)?.[1];
  const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);
  return (
    <header className="sticky top-0 z-30 flex h-13 items-center gap-2 border-b border-line bg-bg/85 px-4 backdrop-blur sm:px-6">
      <div className="md:hidden">
        <Logo />
      </div>
      <button
        type="button"
        onClick={() => setPaletteOpen(true)}
        className="ml-auto flex h-8.5 items-center gap-2 rounded-md border border-line-strong bg-surface px-2.5 text-[13px] text-fg-3 hover:text-fg-2 md:ml-0 md:w-80"
        aria-label="Search or jump to…"
      >
        <Search className="size-3.5" aria-hidden />
        <span className="hidden flex-1 text-left sm:inline">Search or jump to…</span>
        <span className="hidden gap-0.5 sm:flex">
          <Kbd>{isMac ? "⌘" : "Ctrl"}</Kbd>
          <Kbd>K</Kbd>
        </span>
      </button>
      <div className="flex items-center gap-1 md:ml-auto">
        <Button variant="primary" size="sm" onClick={() => newTask({ project_key: projectKey })} title="New task (C)">
          <Plus className="size-3.5" aria-hidden />
          <span className="hidden sm:inline">New task</span>
        </Button>
        <ThemeToggle />
        <div className="md:hidden">
          <Menu
            label="Account"
            triggerClassName="grid size-8.5 place-items-center rounded-md text-fg-2 hover:bg-surface-2"
            trigger={<Settings className="size-4" />}
            items={[
              { label: "Settings", icon: <Settings />, onSelect: () => navigate("/settings") },
              {
                label: "Sign out",
                icon: <LogOut />,
                onSelect: () => logout.mutate(undefined, { onSettled: () => navigate("/login") }),
              },
            ]}
          />
        </div>
      </div>
    </header>
  );
}

function MobileNav() {
  const location = useLocation();
  const [moreOpen, setMoreOpen] = useState(false);
  const waiting = useWaitingRuns();
  const more = NAV.filter((n) => !n.mobile);
  const moreActive = [...more.map((n) => n.to), "/settings"].some((to) => location.pathname.startsWith(to));

  // Any navigation closes the sheet.
  useEffect(() => setMoreOpen(false), [location.pathname]);
  useEffect(() => {
    if (!moreOpen) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setMoreOpen(false);
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [moreOpen]);

  const tab = (isActive: boolean) =>
    clsx("flex h-14 flex-col items-center justify-center gap-0.5 text-[10.5px]", isActive ? "text-fg" : "text-fg-3");

  return (
    <>
      {moreOpen ? (
        <div className="fixed inset-0 z-30 md:hidden" onClick={() => setMoreOpen(false)} aria-hidden>
          <div className="absolute inset-0 bg-overlay" />
        </div>
      ) : null}
      {moreOpen ? (
        <nav
          id="mobile-more"
          aria-label="More"
          className="animate-up fixed inset-x-3 bottom-[calc(3.5rem+env(safe-area-inset-bottom)+0.5rem)] z-40 grid grid-cols-3 gap-1 rounded-xl border border-line-strong bg-surface p-2 shadow-pop md:hidden"
        >
          {[...more, { to: "/settings", label: "Settings", short: "Settings", icon: Settings, end: false, mobile: false }].map(
            ({ to, short, icon: Icon }) => (
              <NavLink
                key={to}
                to={to}
                className={({ isActive }) =>
                  clsx(
                    "flex flex-col items-center gap-1 rounded-lg px-2 py-3 text-[12px]",
                    isActive ? "bg-surface-3 text-fg" : "text-fg-2 hover:bg-surface-2",
                  )
                }
              >
                <Icon className="size-5" aria-hidden />
                {short}
              </NavLink>
            ),
          )}
        </nav>
      ) : null}
      <nav
        aria-label="Main"
        className="safe-bottom fixed inset-x-0 bottom-0 z-40 grid grid-cols-6 border-t border-line bg-surface/95 backdrop-blur md:hidden"
      >
        {NAV.filter((n) => n.mobile).map(({ to, short, icon: Icon, end }) => (
          <NavLink key={to} to={to} end={end} className={({ isActive }) => tab(isActive && !moreOpen)}>
            <span className="relative">
              <Icon className="size-5" aria-hidden />
              {to === "/agents" ? <WaitingBadge count={waiting} compact /> : null}
            </span>
            {short}
          </NavLink>
        ))}
        <button
          type="button"
          onClick={() => setMoreOpen((o) => !o)}
          aria-expanded={moreOpen}
          aria-controls="mobile-more"
          className={tab(moreOpen || moreActive)}
        >
          <Ellipsis className="size-5" aria-hidden />
          More
        </button>
      </nav>
    </>
  );
}
