// Thin fetch wrapper for the same-origin Forge API.
//
// Two rules from the contract live here so no call site can forget them:
//   - every non-GET request carries `X-Forge-Client: web` (the CSRF guard: a
//     custom header forces a CORS preflight, so no other origin can send it);
//   - errors arrive as {"error":{"code","message","field?"}} and become an
//     ApiError, so UI code switches on a stable `code`, never on prose.

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly field?: string;

  constructor(status: number, code: string, message: string, field?: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.field = field;
  }
}

/** Fired on any 401 outside the auth routes; the auth provider listens. */
export const UNAUTHORIZED_EVENT = "forge:unauthorized";

type Query = Record<string, string | number | boolean | undefined | null | string[]>;

export function buildQuery(query?: Query): string {
  if (!query) return "";
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === "" || value === false) continue;
    if (Array.isArray(value)) {
      if (value.length) params.set(key, value.join(","));
      continue;
    }
    params.set(key, value === true ? "1" : String(value));
  }
  const s = params.toString();
  return s ? `?${s}` : "";
}

async function parseError(res: Response): Promise<ApiError> {
  let code = res.status === 401 ? "unauthorized" : "internal";
  let message = res.statusText || `HTTP ${res.status}`;
  let field: string | undefined;
  try {
    const body = await res.json();
    if (body && typeof body === "object" && body.error) {
      code = body.error.code ?? code;
      message = body.error.message ?? message;
      field = body.error.field;
    }
  } catch {
    /* non-JSON error body (proxy page, empty): keep the status-derived defaults */
  }
  return new ApiError(res.status, code, message, field);
}

// ── Elevation (step-up auth) ────────────────────────────────────────────────
//
// Some actions (revealing a secret, deleting a vault item, starting 2FA setup)
// answer 403 `elevation_required` unless the password was re-entered in the
// last few minutes. The request layer handles that once, for every caller: it
// asks the registered handler (the "Confirm it's you" dialog), and if the user
// confirms, retries the original request exactly once. Concurrent requests
// that hit the same wall share one dialog.

type ElevationHandler = () => Promise<boolean>;
let elevationHandler: ElevationHandler | null = null;
let pendingElevation: Promise<boolean> | null = null;

export function setElevationHandler(handler: ElevationHandler | null) {
  elevationHandler = handler;
}

function requestElevation(): Promise<boolean> {
  if (!elevationHandler) return Promise.resolve(false);
  if (!pendingElevation) {
    pendingElevation = elevationHandler().finally(() => {
      pendingElevation = null;
    });
  }
  return pendingElevation;
}

/**
 * Ask for elevation up front — for things that cannot receive the 403 JSON,
 * like a WebSocket upgrade. Resolves true once the user has confirmed.
 */
export function promptElevation(): Promise<boolean> {
  return requestElevation();
}

// `elevate: false` lets background polling fail quietly with the 403 instead
// of popping a password dialog nobody asked for.
type RequestOptions = { body?: unknown; query?: Query; signal?: AbortSignal; elevate?: boolean };

/** Send a request and return the successful Response; every failure becomes an ApiError. */
async function send(method: string, path: string, options: RequestOptions, accept: string, retried = false): Promise<Response> {
  const headers: Record<string, string> = { Accept: accept };
  if (method !== "GET") headers["X-Forge-Client"] = "web";
  if (options.body !== undefined) headers["Content-Type"] = "application/json";

  let res: Response;
  try {
    res = await fetch(`/api${path}${buildQuery(options.query)}`, {
      method,
      headers,
      credentials: "same-origin",
      body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
      signal: options.signal,
    });
  } catch (err) {
    if ((err as Error)?.name === "AbortError") throw err;
    throw new ApiError(0, "network", "Can't reach the Forge API — check your connection.");
  }

  if (res.ok) return res;
  const error = await parseError(res);
  if (res.status === 401 && !path.startsWith("/auth/")) {
    window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
  }
  if (
    res.status === 403 &&
    error.code === "elevation_required" &&
    !retried &&
    options.elevate !== false &&
    path !== "/auth/elevate"
  ) {
    if (await requestElevation()) return send(method, path, options, accept, true);
  }
  throw error;
}

export async function request<T>(method: string, path: string, options: RequestOptions = {}): Promise<T> {
  const res = await send(method, path, options, "application/json");
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

/** GET a file. The filename comes from Content-Disposition when present. */
export async function requestBlob(path: string): Promise<{ blob: Blob; filename: string | null }> {
  const res = await send("GET", path, {}, "*/*");
  return { blob: await res.blob(), filename: filenameFromDisposition(res.headers.get("Content-Disposition")) };
}

export function filenameFromDisposition(header: string | null): string | null {
  if (!header) return null;
  const star = /filename\*\s*=\s*(?:UTF-8'')?([^;]+)/i.exec(header);
  if (star) {
    try {
      return decodeURIComponent(star[1].trim().replace(/^"|"$/g, ""));
    } catch {
      /* fall through to the plain parameter */
    }
  }
  const plain = /filename\s*=\s*"?([^";]+)"?/i.exec(header);
  return plain ? plain[1].trim() : null;
}

export const api = {
  get: <T>(path: string, query?: Query, signal?: AbortSignal) => request<T>("GET", path, { query, signal }),
  /** GET that never prompts for elevation; a 403 elevation_required just throws. */
  getQuiet: <T>(path: string, query?: Query, signal?: AbortSignal) =>
    request<T>("GET", path, { query, signal, elevate: false }),
  post: <T>(path: string, body?: unknown) => request<T>("POST", path, { body: body ?? {} }),
  patch: <T>(path: string, body: unknown) => request<T>("PATCH", path, { body }),
  put: <T>(path: string, body: unknown) => request<T>("PUT", path, { body }),
  del: <T = void>(path: string) => request<T>("DELETE", path),
};

export const isElevationError = (err: unknown) => err instanceof ApiError && err.code === "elevation_required";

export function errorMessage(err: unknown): string {
  if (err instanceof ApiError && err.code === "elevation_required") return "Cancelled — that action needs your password again.";
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error) return err.message;
  return "Something went wrong.";
}
