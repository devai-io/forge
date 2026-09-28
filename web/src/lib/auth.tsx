// Session state for the SPA.
//
// There is no token in the browser: the API sets an HttpOnly cookie and
// `GET /api/auth/me` answers who that is. `null` means signed out. Any 401
// from a data route (session expired, revoked from another device) flips the
// cached user to null, and RequireAuth sends the browser to /login with the
// place it was trying to reach.

import { useQueryClient } from "@tanstack/react-query";
import { useEffect, type ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { ApiError, UNAUTHORIZED_EVENT } from "@/api/client";
import { keys, useMe } from "@/api/hooks";
import type { User } from "@/api/types";
import { FullPageSpinner } from "@/components/ui/Spinner";
import { applyAccent } from "@/lib/accent";

export function useAuthListener() {
  const qc = useQueryClient();
  useEffect(() => {
    const onUnauthorized = () => {
      qc.setQueryData(keys.me, null);
    };
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
  }, [qc]);
}

/**
 * Paints the account's accent whenever /me loads or changes, and caches it for
 * the next first paint. Signed out (or an API without the field), the accent
 * theme-init.js painted from that cache stays.
 */
export function useAccentSync() {
  const accent = useMe().data?.user.accent;
  useEffect(() => {
    if (accent !== undefined) applyAccent(accent);
  }, [accent]);
}

/**
 * The signed-in user, or null. Undefined while the first check is in flight.
 * `failed` is a non-401 failure (API down, network): that is not "signed out",
 * and sending the user to the login form would only make them retype a
 * password the server cannot check either.
 */
export function useCurrentUser(): { user: User | null | undefined; loading: boolean; failed: boolean; retry: () => void } {
  const me = useMe();
  const retry = () => void me.refetch();
  if (me.data !== undefined) return { user: me.data?.user ?? null, loading: false, failed: false, retry };
  if (me.isPending) return { user: undefined, loading: true, failed: false, retry };
  const unauthorized = me.error instanceof ApiError && me.error.status === 401;
  return { user: null, loading: false, failed: !unauthorized, retry };
}

/** When the last password re-entry stops counting for step-up actions (null = not elevated). */
export function useElevatedUntil(): string | null {
  return useMe().data?.elevated_until ?? null;
}

/** The user inside the authenticated shell, where it always exists. */
export function useUser(): User {
  const { user } = useCurrentUser();
  if (!user) throw new Error("useUser outside an authenticated route");
  return user;
}

export function nextParam(pathname: string, search: string): string {
  const target = `${pathname}${search}`;
  return target && target !== "/" ? `?next=${encodeURIComponent(target)}` : "";
}

/** Only same-app paths are honoured — never an absolute URL (open redirect). */
export function safeNext(raw: string | null): string {
  if (!raw || !raw.startsWith("/") || raw.startsWith("//") || raw.startsWith("/\\")) return "/";
  return raw;
}

export function RequireAuth({ children }: { children: ReactNode }) {
  const { user, loading, failed, retry } = useCurrentUser();
  const location = useLocation();
  if (loading) return <FullPageSpinner />;
  if (failed) {
    return (
      <div className="grid min-h-dvh place-items-center p-6 text-center">
        <div className="space-y-3">
          <p className="text-fg-2">Can't reach the Forge API.</p>
          <button
            type="button"
            onClick={retry}
            className="rounded-md border border-line-strong px-3 py-1.5 text-sm hover:bg-surface-2"
          >
            Try again
          </button>
        </div>
      </div>
    );
  }
  if (!user) return <Navigate to={`/login${nextParam(location.pathname, location.search)}`} replace />;
  return <>{children}</>;
}
