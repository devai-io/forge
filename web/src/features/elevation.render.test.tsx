import { fireEvent, screen, waitFor } from "@testing-library/react";
import { api } from "@/api/client";
import { ElevationGate } from "@/features/auth/ElevationGate";
import { jsonResponse, makeUser, renderWithProviders, routeFetch, testClient } from "@/test/render";

// The "Confirm it's you" dialog asks for the authenticator code only on a
// browser that has not passed two-factor before.
describe("ElevationGate", () => {
  afterEach(() => vi.unstubAllGlobals());

  const open = (trusted: boolean) => {
    let elevated = false;
    const fetch = routeFetch({
      "GET /vault/1/file": () =>
        elevated ? jsonResponse(200, {}) : jsonResponse(403, { error: { code: "elevation_required", message: "confirm" } }),
      "POST /auth/elevate": (init) => {
        const body = JSON.parse(String(init.body));
        if (trusted && !body.code) {
          elevated = true;
          return jsonResponse(200, { elevated_until: "2026-10-29T10:10:00Z" });
        }
        if (!body.code) return jsonResponse(422, { error: { code: "validation", message: "wrong or reused code", field: "code" } });
        elevated = true;
        return jsonResponse(200, { elevated_until: "2026-10-29T10:10:00Z" });
      },
    });
    vi.stubGlobal("fetch", fetch);
    const qc = testClient({ user: makeUser({ totp_enabled: true }), elevated_until: null, device_trusted: trusted });
    renderWithProviders(<ElevationGate />, { qc });
    const done = api.get("/vault/1/file");
    return { fetch, done };
  };

  it("a trusted browser confirms with the password alone", async () => {
    const { fetch, done } = open(true);
    fireEvent.change(await screen.findByLabelText("Password"), { target: { value: "correct horse" } });
    expect(screen.queryByLabelText("Authenticator code")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await done;
    const call = fetch.mock.calls.find(([url]) => String(url).includes("/auth/elevate"))!;
    expect(JSON.parse(String(call[1]!.body))).toEqual({ password: "correct horse" });
  });

  it("a new browser is asked for the code", async () => {
    const { done } = open(false);
    await screen.findByLabelText("Password");
    expect(screen.getByLabelText("Authenticator code")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Use a passkey" })).toBeNull(); // no passkey on this account
    await waitFor(() => expect(screen.getByRole("button", { name: "Confirm" })).toBeDisabled());
    // Close it: the client keeps one pending prompt for every request waiting on it.
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await expect(done).rejects.toThrow();
  });

  it("an account with a passkey confirms with it", async () => {
    let elevated = false;
    const finish: unknown[] = [];
    vi.stubGlobal("PublicKeyCredential", function PublicKeyCredential() {});
    const buf = (b: number) => new Uint8Array([b]).buffer;
    vi.stubGlobal("navigator", {
      ...navigator,
      credentials: {
        create: vi.fn(),
        get: vi.fn(async () => ({
          id: "AQ",
          rawId: buf(1),
          type: "public-key",
          response: { clientDataJSON: buf(2), authenticatorData: buf(3), signature: buf(4), userHandle: null },
          getClientExtensionResults: () => ({}),
        })),
      },
    });
    vi.stubGlobal(
      "fetch",
      routeFetch({
        "GET /vault/1/file": () =>
          elevated ? jsonResponse(200, {}) : jsonResponse(403, { error: { code: "elevation_required", message: "confirm" } }),
        "POST /auth/elevate/passkey/begin": () =>
          jsonResponse(200, { ceremony: "e1", publicKey: { challenge: "AQID", allowCredentials: [{ id: "AQ", type: "public-key" }] } }),
        "POST /auth/elevate/passkey/finish": (init) => {
          finish.push(JSON.parse(String(init.body)));
          elevated = true;
          return jsonResponse(200, { elevated_until: "2026-10-29T10:10:00Z" });
        },
      }),
    );
    const qc = testClient({ user: makeUser({ totp_enabled: true }), elevated_until: null, device_trusted: false, passkeys: 1 });
    renderWithProviders(<ElevationGate />, { qc });
    const done = api.get("/vault/1/file");
    fireEvent.click(await screen.findByRole("button", { name: "Use a passkey" }));
    await done;
    expect(finish).toHaveLength(1);
    expect((finish[0] as { ceremony: string }).ceremony).toBe("e1");
  });
});
