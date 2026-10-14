// "Open shell" on a server: a tmux session on one of your machines that runs
// ssh to the server, then attaches to it. The full button (server page) lets
// you pick the machine when several are online; the icon (server cards) uses
// the usual one.

import { SquareTerminal } from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useServerShell, useTerminalHosts } from "@/api/hooks";
import type { Server } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { Select } from "@/components/ui/Input";
import { useToast } from "@/components/ui/Toast";
import { attachPath, pickHost, readStoredHost, storeHost } from "@/lib/terminal";

function useOpenShell(s: Server) {
  const hosts = useTerminalHosts({ refetchInterval: false });
  const shell = useServerShell();
  const toast = useToast();
  const navigate = useNavigate();
  const [picked, setPicked] = useState<string>("");
  const online = (hosts.data?.hosts ?? []).filter((h) => h.terminal && h.online);
  const effective =
    picked ||
    String(
      pickHost(
        online.map((h) => h.runner_id),
        readStoredHost(),
        hosts.data?.default_runner_id ?? null,
      ) ?? "",
    );
  const none = !!hosts.data && online.length === 0;
  const open = () =>
    shell.mutate(
      { id: s.id, runnerId: effective ? Number(effective) : undefined },
      {
        onSuccess: ({ runner_id, session }) => {
          storeHost(runner_id);
          navigate(attachPath(runner_id, session));
        },
        onError: (err) => toast.error(err),
      },
    );
  const from = online.find((h) => String(h.runner_id) === effective)?.runner_name;
  const title = none ? "No machine with terminals is connected" : `ssh ${s.ssh_target || s.name}${from ? ` from ${from}` : ""}`;
  return { online, effective, setPicked, none, open, pending: shell.isPending, title };
}

export function ShellButton({ server }: { server: Server }) {
  const sh = useOpenShell(server);
  return (
    <div className="flex items-center gap-1.5">
      {sh.online.length > 1 ? (
        <Select compact aria-label="Machine to open the shell from" value={sh.effective} onChange={(e) => sh.setPicked(e.target.value)} className="w-36">
          {sh.online.map((h) => (
            <option key={h.runner_id} value={h.runner_id}>
              from {h.runner_name}
            </option>
          ))}
        </Select>
      ) : null}
      <Button size="sm" variant="primary" onClick={sh.open} loading={sh.pending} disabled={sh.none} title={sh.title}>
        {!sh.pending ? <SquareTerminal className="size-3.5" aria-hidden /> : null} Open shell
      </Button>
    </div>
  );
}

export function ShellIconButton({ server }: { server: Server }) {
  const sh = useOpenShell(server);
  return (
    <Button
      size="icon-sm"
      variant="ghost"
      aria-label={`Open a shell on ${server.name}`}
      title={sh.title}
      onClick={sh.open}
      loading={sh.pending}
      disabled={sh.none}
    >
      {!sh.pending ? <SquareTerminal className="size-3.5" /> : null}
    </Button>
  );
}
