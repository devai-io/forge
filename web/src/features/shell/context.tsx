// App-wide actions any screen can trigger: open a task (a `?task=` search
// param, so the drawer is deep-linkable and survives a reload), create a task,
// queue an agent run, open the command palette. The dialogs live here once
// instead of being mounted by every page that has a "New" button.

import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import type { Run, TaskStatus } from "@/api/types";
import { NewRunDialog } from "@/features/agents/NewRunDialog";
import { NewTaskDialog } from "@/features/tasks/NewTaskDialog";
import { NewSessionDialog } from "@/features/terminal/NewSessionDialog";

export type NewTaskPrefill = { project_key?: string; status?: TaskStatus; focus?: boolean };
export type NewSessionPrefill = {
  runner_id?: number;
  project_key?: string;
  repo_id?: number;
  start?: "claude" | "shell";
};

export type NewRunPrefill = {
  project_key?: string;
  repo_id?: number;
  task_id?: number | null;
  task_ref?: string | null;
  prompt?: string;
  resume?: Run;
};

type ShellActions = {
  openTask: (id: number) => void;
  closeTask: () => void;
  taskId: number | null;
  newTask: (prefill?: NewTaskPrefill) => void;
  newRun: (prefill?: NewRunPrefill) => void;
  newSession: (prefill?: NewSessionPrefill) => void;
  paletteOpen: boolean;
  setPaletteOpen: (open: boolean) => void;
};

const ShellContext = createContext<ShellActions | null>(null);

export function ShellProvider({ children }: { children: ReactNode }) {
  const [params, setParams] = useSearchParams();
  const [taskPrefill, setTaskPrefill] = useState<NewTaskPrefill | null>(null);
  const [runPrefill, setRunPrefill] = useState<NewRunPrefill | null>(null);
  const [sessionPrefill, setSessionPrefill] = useState<NewSessionPrefill | null>(null);
  const [paletteOpen, setPaletteOpen] = useState(false);

  const raw = Number(params.get("task"));
  const taskId = Number.isInteger(raw) && raw > 0 ? raw : null;

  const openTask = useCallback(
    (id: number) =>
      setParams((p) => {
        const next = new URLSearchParams(p);
        next.set("task", String(id));
        return next;
      }),
    [setParams],
  );
  const closeTask = useCallback(
    () =>
      setParams((p) => {
        const next = new URLSearchParams(p);
        next.delete("task");
        return next;
      }),
    [setParams],
  );

  const value = useMemo<ShellActions>(
    () => ({
      openTask,
      closeTask,
      taskId,
      newTask: (prefill) => setTaskPrefill(prefill ?? {}),
      newRun: (prefill) => setRunPrefill(prefill ?? {}),
      newSession: (prefill) => setSessionPrefill(prefill ?? {}),
      paletteOpen,
      setPaletteOpen,
    }),
    [openTask, closeTask, taskId, paletteOpen],
  );

  return (
    <ShellContext.Provider value={value}>
      {children}
      {taskPrefill ? <NewTaskDialog prefill={taskPrefill} onClose={() => setTaskPrefill(null)} /> : null}
      {runPrefill ? <NewRunDialog prefill={runPrefill} onClose={() => setRunPrefill(null)} /> : null}
      {sessionPrefill ? <NewSessionDialog prefill={sessionPrefill} onClose={() => setSessionPrefill(null)} /> : null}
    </ShellContext.Provider>
  );
}

export function useShell(): ShellActions {
  const ctx = useContext(ShellContext);
  if (!ctx) throw new Error("useShell outside ShellProvider");
  return ctx;
}
