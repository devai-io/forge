// Setting up a machine after a fresh `git clone`: the project's repo files
// (git-ignored .env, keys, keystores) as one tar.gz — fetched on that machine
// with a one-time code, or downloaded here. Both are audited downloads.

import { Download, KeyRound, TerminalSquare } from "lucide-react";
import { useEffect, useState } from "react";
import { requestBlob } from "@/api/client";
import { createVaultSetupCode } from "@/api/hooks";
import type { VaultSetupCode } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Select } from "@/components/ui/Input";
import { useToast } from "@/components/ui/Toast";
import { CommandBlock } from "@/features/agents/PairingDialog";
import { pairingCountdown } from "@/lib/agents";
import { saveBlob } from "@/lib/vault";

export function MachineSetupDialog({
  projectKey,
  repos,
  fileCount,
  onClose,
}: {
  projectKey: string;
  repos: { id: number; name: string }[];
  fileCount: number;
  onClose: () => void;
}) {
  const toast = useToast();
  const [repoId, setRepoId] = useState("");
  const [code, setCode] = useState<VaultSetupCode | null>(null);
  const [busy, setBusy] = useState<"code" | "download" | null>(null);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!code) return;
    const t = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(t);
  }, [code]);
  const countdown = code ? pairingCountdown(code.expires_at, now) : null;
  const repoQuery = repoId ? `?repo=${repoId}` : "";

  const makeCode = async () => {
    setBusy("code");
    try {
      setCode(await createVaultSetupCode(projectKey, repoId ? Number(repoId) : undefined));
      setNow(Date.now());
    } catch (err) {
      toast.error(err);
    } finally {
      setBusy(null);
    }
  };
  const download = async () => {
    setBusy("download");
    try {
      const { blob, filename } = await requestBlob(`/projects/${projectKey}/vault/bundle${repoQuery}`, "POST");
      saveBlob(blob, filename || `${projectKey.toLowerCase()}-secrets.tar.gz`);
    } catch (err) {
      toast.error(err);
    } finally {
      setBusy(null);
    }
  };

  return (
    <Dialog
      open
      onClose={onClose}
      size="lg"
      title="Set up a machine"
      description={`Clone ${projectKey}'s repos, then put back what git ignores: ${fileCount} repo file${fileCount === 1 ? "" : "s"} in the vault.`}
      footer={
        <Button variant="ghost" onClick={onClose}>
          Close
        </Button>
      }
    >
      <div className="space-y-4 text-[13px]">
        {repos.length > 1 ? (
          <label className="flex flex-wrap items-center gap-2">
            <span className="text-fg-2">Repos</span>
            <Select
              aria-label="Repo"
              className="w-auto"
              value={repoId}
              onChange={(e) => {
                setRepoId(e.target.value);
                setCode(null);
              }}
            >
              <option value="">All of them</option>
              {repos.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
            </Select>
          </label>
        ) : null}

        <section aria-labelledby="setup-code" className="space-y-2 rounded-lg border border-line p-3">
          <h3 id="setup-code" className="flex items-center gap-1.5 font-semibold">
            <TerminalSquare className="size-4 text-fg-3" aria-hidden /> On that machine
          </h3>
          <p className="text-fg-2">
            Run this in the folder that holds the checkouts (each repo&apos;s files land in its own folder). It works once,
            for ten minutes, and needs no login; it overwrites files that are already there.
          </p>
          {code && countdown ? (
            <>
              <CommandBlock command={code.command} />
              <p className="text-[12px] text-fg-3">Expires in {countdown}.</p>
            </>
          ) : (
            <Button variant="primary" size="sm" onClick={makeCode} loading={busy === "code"}>
              <KeyRound className="size-3.5" aria-hidden /> {code ? "New one-time command" : "Make a one-time command"}
            </Button>
          )}
        </section>

        <section aria-labelledby="setup-download" className="space-y-2 rounded-lg border border-line p-3">
          <h3 id="setup-download" className="flex items-center gap-1.5 font-semibold">
            <Download className="size-4 text-fg-3" aria-hidden /> Or download it here
          </h3>
          <p className="text-fg-2">
            The same archive: <code className="font-mono text-[12px]">tar -xzvf {projectKey.toLowerCase()}-secrets.tar.gz</code> next to
            the checkouts.
          </p>
          <Button size="sm" variant="subtle" onClick={download} loading={busy === "download"}>
            <Download className="size-3.5" aria-hidden /> Download .tar.gz
          </Button>
        </section>

        <p className="text-[12px] text-fg-3">
          Missing something? On a machine that has it, run <code className="font-mono">forge agent secrets push</code> inside the
          repo: it finds the git-ignored secret files and stores them here.
        </p>
      </div>
    </Dialog>
  );
}
