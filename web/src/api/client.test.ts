import { ApiError, api, buildQuery, UNAUTHORIZED_EVENT } from "./client";

function mockFetch(status: number, body?: unknown) {
  const fn = vi.fn(async () =>
    new Response(body === undefined ? null : JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    }),
  );
  vi.stubGlobal("fetch", fn);
  return fn;
}

afterEach(() => vi.unstubAllGlobals());

describe("buildQuery", () => {
  it("drops empty values and joins arrays", () => {
    expect(buildQuery({ project: "SHOP", status: ["todo", "blocked"], focus: true, q: "", open: false, x: undefined })).toBe(
      "?project=SHOP&status=todo%2Cblocked&focus=1",
    );
    expect(buildQuery({})).toBe("");
  });
});

describe("request", () => {
  it("sends the CSRF header on writes only", async () => {
    const fetch = mockFetch(200, { ok: true });
    await api.get("/dashboard");
    await api.post("/tasks", { title: "x" });
    const getHeaders = (fetch.mock.calls[0] as unknown as [string, RequestInit])[1].headers as Record<string, string>;
    const postInit = (fetch.mock.calls[1] as unknown as [string, RequestInit])[1];
    expect(getHeaders["X-Forge-Client"]).toBeUndefined();
    expect((postInit.headers as Record<string, string>)["X-Forge-Client"]).toBe("web");
    expect(postInit.body).toBe(JSON.stringify({ title: "x" }));
    expect((fetch.mock.calls[0] as unknown as [string])[0]).toBe("/api/dashboard");
  });

  it("turns the error envelope into an ApiError", async () => {
    mockFetch(422, { error: { code: "validation", message: "title is required", field: "title" } });
    const err = await api.post("/tasks", {}).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 422, code: "validation", message: "title is required", field: "title" });
  });

  it("announces a 401 on data routes but not on auth routes", async () => {
    const seen = vi.fn();
    window.addEventListener(UNAUTHORIZED_EVENT, seen);
    mockFetch(401, { error: { code: "unauthorized", message: "no session" } });
    await api.get("/dashboard").catch(() => {});
    await api.post("/auth/login", {}).catch(() => {});
    window.removeEventListener(UNAUTHORIZED_EVENT, seen);
    expect(seen).toHaveBeenCalledTimes(1);
  });

  it("returns undefined for 204", async () => {
    mockFetch(204);
    await expect(api.del("/tasks/1")).resolves.toBeUndefined();
  });
});
