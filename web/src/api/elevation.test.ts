import { ApiError, api, filenameFromDisposition, setElevationHandler } from "./client";

const json = (status: number, body?: unknown) =>
  new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const needsElevation = () => json(403, { error: { code: "elevation_required", message: "confirm it's you" } });

afterEach(() => {
  vi.unstubAllGlobals();
  setElevationHandler(null);
});

describe("elevation_required", () => {
  it("asks once, then retries the original request", async () => {
    const fetch = vi.fn().mockResolvedValueOnce(needsElevation()).mockResolvedValueOnce(json(200, { secret: { value: "s3cret" } }));
    vi.stubGlobal("fetch", fetch);
    const handler = vi.fn().mockResolvedValue(true);
    setElevationHandler(handler);
    await expect(api.post("/vault/1/reveal")).resolves.toEqual({ secret: { value: "s3cret" } });
    expect(handler).toHaveBeenCalledTimes(1);
    expect(fetch).toHaveBeenCalledTimes(2);
    expect((fetch.mock.calls[1] as [string])[0]).toBe("/api/vault/1/reveal");
  });

  it("surfaces the 403 when the user cancels", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(needsElevation()));
    setElevationHandler(() => Promise.resolve(false));
    const err = await api.del("/vault/1").catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.code).toBe("elevation_required");
  });

  it("retries only once, even if the retry is refused again", async () => {
    const fetch = vi.fn().mockImplementation(async () => needsElevation());
    vi.stubGlobal("fetch", fetch);
    const handler = vi.fn().mockResolvedValue(true);
    setElevationHandler(handler);
    await expect(api.post("/vault/1/reveal")).rejects.toMatchObject({ code: "elevation_required" });
    expect(handler).toHaveBeenCalledTimes(1);
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("shares one prompt between concurrent requests", async () => {
    let calls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (url: string) => {
        calls++;
        return calls <= 2 ? needsElevation() : json(200, { url });
      }),
    );
    let release!: (ok: boolean) => void;
    const handler = vi.fn(() => new Promise<boolean>((r) => (release = r)));
    setElevationHandler(handler);
    const a = api.post("/vault/1/reveal");
    const b = api.post("/vault/2/reveal");
    await vi.waitFor(() => expect(handler).toHaveBeenCalled());
    release(true);
    await expect(Promise.all([a, b])).resolves.toHaveLength(2);
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it("never loops on the elevate route itself", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(needsElevation()));
    const handler = vi.fn().mockResolvedValue(true);
    setElevationHandler(handler);
    await expect(api.post("/auth/elevate", { password: "x" })).rejects.toBeInstanceOf(ApiError);
    expect(handler).not.toHaveBeenCalled();
  });
});

describe("filenameFromDisposition", () => {
  it("reads plain and RFC 5987 filenames", () => {
    expect(filenameFromDisposition('attachment; filename="upload.jks"')).toBe("upload.jks");
    expect(filenameFromDisposition("attachment; filename*=UTF-8''AuthKey%20ABC.p8")).toBe("AuthKey ABC.p8");
    expect(filenameFromDisposition("attachment")).toBeNull();
    expect(filenameFromDisposition(null)).toBeNull();
  });
});
