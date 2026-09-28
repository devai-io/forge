import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router-dom";
import type { MeResponse, User } from "@/api/types";
import { ToastProvider } from "@/components/ui/Toast";
import { ThemeProvider } from "@/lib/theme";

export function makeUser(overrides: Partial<User> = {}): User {
  return {
    id: 1,
    username: "ada",
    email: "ada@example.com",
    display_name: "Ada",
    timezone: "Europe/Lisbon",
    weekly_goal: 15,
    created_at: "2026-09-01T00:00:00Z",
    totp_enabled: false,
    checkup_time: "08:00",
    checkup_email: true,
    accent: "",
    ...overrides,
  };
}

export function testClient(me: MeResponse | null = { user: makeUser(), elevated_until: null }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } });
  qc.setQueryData(["me"], me);
  return qc;
}

export function renderWithProviders(ui: ReactNode, { qc = testClient(), route = "/" }: { qc?: QueryClient; route?: string } = {}) {
  return {
    qc,
    ...render(
      <ThemeProvider>
        <QueryClientProvider client={qc}>
          <MemoryRouter initialEntries={[route]}>
            <ToastProvider>{ui}</ToastProvider>
          </MemoryRouter>
        </QueryClientProvider>
      </ThemeProvider>,
    ),
  };
}

/** A fetch stub routed by "METHOD /path" (query string ignored). */
export function routeFetch(routes: Record<string, (init: RequestInit) => Response | Promise<Response>>) {
  return vi.fn(async (url: string, init: RequestInit = {}) => {
    const path = url.split("?")[0].replace(/^\/api/, "");
    const handler = routes[`${init.method ?? "GET"} ${path}`];
    if (!handler) return new Response(JSON.stringify({ error: { code: "not_found", message: path } }), { status: 404 });
    return handler(init);
  });
}

export const jsonResponse = (status: number, body?: unknown) =>
  new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
