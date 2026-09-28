import { awaitingPairing, defaultNewRole, pairCommands, pairingCountdown, pairingState, runnerConfigSnippet } from "./agents";
import { formatBytes } from "./format";
import { gettingStartedSteps } from "./gettingStarted";
import { fileProblem, MAX_FILE_BYTES } from "./projects";

const T0 = Date.parse("2026-09-28T10:00:00Z");
const pairing = { code: "K7QD-M3XP", expires_at: "2026-09-28T10:15:00Z" }; // issued 10:00 server time

describe("pairingCountdown", () => {
  it("counts down as m:ss and stops at expiry", () => {
    expect(pairingCountdown(pairing.expires_at, T0)).toBe("15:00");
    expect(pairingCountdown(pairing.expires_at, T0 + 60_000 * 2 + 26_000)).toBe("12:34");
    expect(pairingCountdown(pairing.expires_at, T0 + 15 * 60_000 - 400)).toBe("0:01");
    expect(pairingCountdown(pairing.expires_at, T0 + 15 * 60_000)).toBeNull();
    expect(pairingCountdown("garbage", T0)).toBeNull();
  });
});

describe("pairingState", () => {
  it("waits while the code is out, then expires", () => {
    const row = { last_seen_at: null, pair_expires_at: pairing.expires_at };
    expect(pairingState(row, pairing, T0 + 60_000)).toBe("waiting");
    expect(pairingState(row, pairing, T0 + 16 * 60_000)).toBe("expired");
    expect(pairingState(undefined, pairing, T0)).toBe("waiting");
  });
  it("is paired once the code is used, connected once heard from since it was issued", () => {
    expect(pairingState({ last_seen_at: null, pair_expires_at: null }, pairing, T0 + 60_000)).toBe("paired");
    expect(pairingState({ last_seen_at: "2026-09-28T10:02:00Z", pair_expires_at: null }, pairing, T0 + 120_000)).toBe("connected");
  });
  it("does not call a re-paired machine connected on its old heartbeat alone", () => {
    // Old token still heartbeating, code not used yet.
    expect(pairingState({ last_seen_at: "2026-09-28T10:01:00Z", pair_expires_at: pairing.expires_at }, pairing, T0 + 90_000)).toBe("waiting");
    // Code used, but the last heartbeat predates the code.
    expect(pairingState({ last_seen_at: "2026-09-28T09:50:00Z", pair_expires_at: null }, pairing, T0 + 90_000)).toBe("paired");
  });
});

describe("pairing helpers", () => {
  it("builds both command variants with the origin and code", () => {
    const c = pairCommands("https://forge.example.com", "K7QD-M3XP");
    expect(c.install).toBe(
      "curl -fsSL https://github.com/devai-io/forge/releases/latest/download/install.sh | sh -s -- --pair https://forge.example.com K7QD-M3XP",
    );
    expect(c.pair).toBe("forge agent pair https://forge.example.com K7QD-M3XP");
    expect(c.service).toBe("forge agent install");
    expect(c.claude).toBe("forge agent setup-claude");
  });
  it("makes the first machine the master", () => {
    expect(defaultNewRole([])).toBe("master");
    expect(defaultNewRole([{ role: "worker" }])).toBe("master");
    expect(defaultNewRole([{ role: "worker" }, { role: "master" }])).toBe("worker");
  });
  it("marks never-connected machines with a live code", () => {
    expect(awaitingPairing({ last_seen_at: null, pair_expires_at: pairing.expires_at }, T0)).toBe(true);
    expect(awaitingPairing({ last_seen_at: null, pair_expires_at: pairing.expires_at }, T0 + 20 * 60_000)).toBe(false);
    expect(awaitingPairing({ last_seen_at: "2026-09-28T09:00:00Z", pair_expires_at: pairing.expires_at }, T0)).toBe(false);
    expect(awaitingPairing({ last_seen_at: null, pair_expires_at: null }, T0)).toBe(false);
  });
  it("defaults allowed_roots to ~/dev and the agent's projects folder", () => {
    const cfg = JSON.parse(runnerConfigSnippet("https://forge.example.com", "frg_x"));
    expect(cfg.allowed_roots).toEqual(["~/dev"]);
    expect(cfg.api_url).toBe("https://forge.example.com");
    expect(cfg.token).toBe("frg_x");
  });
});

describe("formatBytes", () => {
  it("prints sizes people read", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(812)).toBe("812 B");
    expect(formatBytes(1024)).toBe("1 KB");
    expect(formatBytes(4300)).toBe("4.2 KB");
    expect(formatBytes(13 * 1024 * 1024)).toBe("13 MB");
    expect(formatBytes(1.1 * 1024 ** 3)).toBe("1.1 GB");
    expect(formatBytes(-1)).toBe("—");
    expect(formatBytes(null)).toBe("—");
  });
});

describe("fileProblem", () => {
  it("refuses what the API would", () => {
    expect(fileProblem({ name: "spec.pdf", size: 10 })).toBeNull();
    expect(fileProblem({ name: ".env", size: 10 })).toMatch(/dot/);
    expect(fileProblem({ name: "a/b.txt", size: 10 })).toMatch(/slash/);
    expect(fileProblem({ name: "big.zip", size: MAX_FILE_BYTES + 1 })).toMatch(/100 MB/);
    expect(fileProblem({ name: "edge.zip", size: MAX_FILE_BYTES })).toBeNull();
    expect(fileProblem({ name: `${"x".repeat(201)}.txt`, size: 1 })).toMatch(/rename/);
  });
});

describe("gettingStartedSteps", () => {
  it("marks each step from what the account has", () => {
    const steps = gettingStartedSteps({ machines: 0, projects: 3, totp: false, docsRead: true });
    expect(steps.map((s) => [s.id, s.done])).toEqual([
      ["machine", false],
      ["project", true],
      ["2fa", false],
      ["docs", true],
    ]);
    expect(steps[0].to).toBe("/agents?add=1");
  });
});
