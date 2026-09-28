// Pairing a machine: a one-time code, the one command to run on that machine,
// and a live line that turns green when the machine checks in. The runner
// list is polled while the dialog is open; the state comes from its row.

import clsx from "clsx";
import { Check, CircleAlert, CircleCheck, Copy } from "lucide-react";
import { useEffect, useState } from "react";
import { usePairRunner, useRunners } from "@/api/hooks";
import type { Pairing, Runner } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Spinner } from "@/components/ui/Spinner";
import { Tabs } from "@/components/ui/Tabs";
import { useToast } from "@/components/ui/Toast";
import { pairCommands, pairingCountdown, pairingState } from "@/lib/agents";

export function CopyButton({ text, label, className }: { text: string; label: string; className?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <Button
      size="sm"
      variant="subtle"
      className={className}
      onClick={() =>
        void navigator.clipboard?.writeText(text).then(() => {
          setCopied(true);
          window.setTimeout(() => setCopied(false), 1500);
        })
      }
    >
      {copied ? <Check className="size-3.5 text-good" aria-hidden /> : <Copy className="size-3.5" aria-hidden />}
      {copied ? "Copied" : label}
    </Button>
  );
}

/** A shell command with its own copy button; wraps instead of scrolling on a phone. */
export function CommandBlock({ command, label = "Copy" }: { command: string; label?: string }) {
  return (
    <div className="flex items-start gap-2 rounded-md border border-line bg-surface-2 py-1.5 pr-1.5 pl-3">
      <code className="min-w-0 flex-1 py-0.5 font-mono text-[12px] break-all whitespace-pre-wrap text-fg">
        <span aria-hidden className="text-fg-3 select-none">
          ${" "}
        </span>
        {command}
      </code>
      <CopyButton text={command} label={label} className="shrink-0" />
    </div>
  );
}

/** Re-renders every second while `active` (the expiry countdown). */
function useSecondTick(active: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    const t = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(t);
  }, [active]);
  return now;
}

type How = "new" | "installed";

export function PairingDialog({ runner: initial, pairing: first, onClose }: { runner: Runner; pairing: Pairing; onClose: () => void }) {
  const [pairing, setPairing] = useState(first);
  const [how, setHow] = useState<How>("new");
  const runners = useRunners(3000);
  const again = usePairRunner();
  const toast = useToast();
  const runner = runners.data?.find((r) => r.id === initial.id) ?? initial;
  const now = useSecondTick(true);
  const state = pairingState(runner, pairing, now);
  const countdown = pairingCountdown(pairing.expires_at, now);
  const cmds = pairCommands(window.location.origin, pairing.code);
  const settled = state === "connected" || state === "paired";

  const newCode = () =>
    again.mutate(initial.id, {
      onSuccess: (res) => setPairing(res.pairing),
      onError: (e) => toast.error(e),
    });

  return (
    <Dialog
      open
      onClose={onClose}
      size="lg"
      title={`Pair ${runner.name}`}
      description="Run one command on that machine. The code works once."
      footer={
        state === "connected" ? (
          <Button variant="primary" onClick={onClose} data-autofocus>
            Done
          </Button>
        ) : state === "expired" ? (
          <>
            <Button variant="ghost" onClick={onClose}>
              Close
            </Button>
            <Button variant="primary" onClick={newCode} loading={again.isPending}>
              New code
            </Button>
          </>
        ) : (
          <Button variant="ghost" onClick={onClose}>
            Close
          </Button>
        )
      }
    >
      <div className="space-y-4 text-[13px]">
        {!settled ? (
          <>
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-line bg-surface-2 px-4 py-3">
              <div className="min-w-0">
                <p className="text-[11.5px] text-fg-3">Pairing code</p>
                <p
                  className={clsx(
                    "font-mono text-[26px] leading-tight font-semibold tracking-[0.12em]",
                    state === "expired" && "text-fg-3 line-through",
                  )}
                >
                  {pairing.code}
                </p>
                <p className="tabular text-[12px] text-fg-3">{countdown ? `expires in ${countdown}` : "expired"}</p>
              </div>
              {state !== "expired" ? <CopyButton text={pairing.code} label="Copy code" /> : null}
            </div>

            {state !== "expired" ? (
              <div className="space-y-3">
                <Tabs<How>
                  label="How to pair"
                  value={how}
                  onChange={setHow}
                  items={[
                    { value: "new", label: "New machine" },
                    { value: "installed", label: "Forge already installed" },
                  ]}
                />
                {how === "new" ? (
                  <div role="tabpanel" aria-label="New machine" className="space-y-2">
                    <CommandBlock command={cmds.install} />
                    <p className="text-fg-3">
                      Installs <code className="font-mono">forge</code> into <code className="font-mono">~/.local/bin</code>,
                      pairs, starts it as a background service and connects Claude Code. Linux and macOS.
                    </p>
                  </div>
                ) : (
                  <div role="tabpanel" aria-label="Forge already installed" className="space-y-2">
                    <CommandBlock command={cmds.pair} />
                    <CommandBlock command={cmds.service} />
                    <p className="text-fg-3">
                      Runs the agent in the background (a systemd user unit on Linux, launchd on macOS). Optionally,{" "}
                      <code className="font-mono">{cmds.claude}</code> adds the Forge MCP server and session hook to
                      Claude Code.
                    </p>
                  </div>
                )}
              </div>
            ) : null}
          </>
        ) : null}

        <div
          role="status"
          aria-live="polite"
          className="flex items-start gap-2.5 rounded-lg border border-line px-3 py-2.5"
        >
          {state === "connected" ? (
            <>
              <CircleCheck className="mt-0.5 size-4 shrink-0 text-good" aria-hidden />
              <span>
                <span className="font-medium">{runner.name} is connected.</span>{" "}
                <span className="text-fg-3">
                  {runner.hostname ? `${runner.hostname}${runner.os ? ` · ${runner.os}` : ""}. ` : ""}It shows up online on this page.
                </span>
              </span>
            </>
          ) : state === "paired" ? (
            <>
              <Spinner className="mt-0.5 size-4 shrink-0" />
              <span>
                <span className="font-medium">Paired.</span>{" "}
                <span className="text-fg-3">
                  Waiting for {runner.name} to check in — start it with <code className="font-mono">forge agent install</code>{" "}
                  if it isn't running yet.
                </span>
              </span>
            </>
          ) : state === "expired" ? (
            <>
              <CircleAlert className="mt-0.5 size-4 shrink-0 text-warning" aria-hidden />
              <span>
                <span className="font-medium">Code expired.</span>{" "}
                <span className="text-fg-3">Get a new one and run the command again.</span>
              </span>
            </>
          ) : (
            <>
              <Spinner className="mt-0.5 size-4 shrink-0" />
              <span className="text-fg-2">Waiting for {runner.name} to pair…</span>
            </>
          )}
        </div>
      </div>
    </Dialog>
  );
}
