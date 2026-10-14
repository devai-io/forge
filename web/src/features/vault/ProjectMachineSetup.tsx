import { FolderGit2, MonitorDown } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router-dom";
import { useVault } from "@/api/hooks";
import type { ProjectDetail } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { Panel } from "@/components/ui/Panel";
import { groupByRepo } from "@/lib/vault";
import { MachineSetupDialog } from "./MachineSetupDialog";

/**
 * The project's git-ignored files kept in the vault, and the way to put them
 * on a fresh clone. Silent when the vault is unavailable.
 */
export function ProjectMachineSetup({ project: p }: { project: Pick<ProjectDetail, "key" | "repos"> }) {
  const vault = useVault({ project: p.key });
  const [open, setOpen] = useState(false);
  if (!vault.data?.available) return null;
  const groups = groupByRepo(vault.data.items);
  const count = groups.reduce((n, g) => n + g.items.length, 0);
  return (
    <Panel
      title="Machine setup"
      icon={<MonitorDown />}
      id="proj-machine-setup"
      actions={
        <Button size="sm" variant="ghost" onClick={() => setOpen(true)} disabled={!count}>
          Set up
        </Button>
      }
    >
      <p className="px-2 py-1 text-[13px] text-fg-2">
        {count ? (
          <>
            <Link to={`/vault?project=${p.key}`} className="font-medium hover:text-accent hover:underline">
              {count} repo file{count === 1 ? "" : "s"}
            </Link>{" "}
            in the vault for a fresh clone
            {groups.length ? (
              <span className="text-fg-3">
                {" "}
                · <FolderGit2 className="inline size-3" aria-hidden /> {groups.map((g) => g.repo).join(", ")}
              </span>
            ) : null}
            .
          </>
        ) : (
          <>
            No repo files yet. Run <code className="font-mono text-[12px]">forge agent secrets push</code> in a checkout to keep
            its .env files and keys in the vault.
          </>
        )}
      </p>
      {open ? (
        <MachineSetupDialog projectKey={p.key} repos={p.repos.map((r) => ({ id: r.id, name: r.name }))} fileCount={count} onClose={() => setOpen(false)} />
      ) : null}
    </Panel>
  );
}
