import { ApiError } from "@/api/client";
import { setupFailure, setupRedirect, timezoneOptions, validateSetup, type SetupForm } from "./setup";

const valid: SetupForm = {
  token: "tok-123",
  username: "ada",
  email: "ada@example.com",
  password: "correct horse battery",
  confirm: "correct horse battery",
  display_name: "",
  timezone: "Europe/Lisbon",
};

describe("setupRedirect", () => {
  it("sends /login to /setup only while setup is needed, keeping the query", () => {
    expect(setupRedirect(true, "?token=abc")).toBe("/setup?token=abc");
    expect(setupRedirect(true, "")).toBe("/setup");
    expect(setupRedirect(false, "?token=abc")).toBeNull();
    expect(setupRedirect(undefined, "")).toBeNull();
  });
});

describe("validateSetup", () => {
  it("accepts a complete form", () => {
    expect(validateSetup(valid)).toEqual({});
  });
  it("flags each missing or malformed field", () => {
    const errors = validateSetup({ ...valid, token: " ", username: "a da", email: "ada", confirm: "nope", timezone: "" });
    expect(validateSetup({ ...valid, username: "a" }).username).toBeDefined();
    expect(validateSetup({ ...valid, username: "ada.l-ove_1" }).username).toBeUndefined();
    expect(Object.keys(errors).sort()).toEqual(["confirm", "email", "timezone", "token", "username"]);
  });
  it("holds passwords to 12–72 characters (bytes, as bcrypt counts them)", () => {
    expect(validateSetup({ ...valid, password: "short", confirm: "short" }).password).toMatch(/12/);
    expect(validateSetup({ ...valid, password: "x".repeat(72), confirm: "x".repeat(72) }).password).toBeUndefined();
    expect(validateSetup({ ...valid, password: "x".repeat(73), confirm: "x".repeat(73) }).password).toMatch(/72/);
    const multi = "ü".repeat(40); // 40 characters, 80 bytes
    expect(validateSetup({ ...valid, password: multi, confirm: multi }).password).toMatch(/72/);
  });
});

describe("setupFailure", () => {
  it("maps the API's answers onto the form", () => {
    expect(setupFailure(new ApiError(409, "setup_done", "already set up"))).toEqual({ kind: "done" });
    expect(setupFailure(new ApiError(403, "bad_setup_token", "no"))).toMatchObject({ kind: "field", field: "token" });
    expect(setupFailure(new ApiError(422, "validation", "username taken", "username"))).toEqual({
      kind: "field",
      field: "username",
      message: "username taken",
    });
    expect(setupFailure(new ApiError(422, "validation", "odd", "something_else"))).toEqual({ kind: "form", message: "odd" });
    expect(setupFailure(new ApiError(429, "rate_limited", "slow down"))).toMatchObject({ kind: "form", message: expect.stringMatching(/Too many/) });
    expect(setupFailure(new Error("boom"))).toEqual({ kind: "form", message: "boom" });
  });
});

describe("timezoneOptions", () => {
  it("always has the current zone and UTC", () => {
    const zones = timezoneOptions("Atlantis/Nowhere");
    expect(zones[0]).toBe("Atlantis/Nowhere");
    expect(zones).toContain("UTC");
    expect(new Set(timezoneOptions("UTC")).size).toBe(timezoneOptions("UTC").length);
  });
});
