// Toasts: short confirmations and errors for mutations. Errors stay longer
// and are announced assertively; everything else politely.

import clsx from "clsx";
import { CircleAlert, CircleCheck, Info, X } from "lucide-react";
import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";
import { errorMessage } from "@/api/client";

type ToastKind = "success" | "error" | "info";
type ToastItem = { id: number; kind: ToastKind; message: ReactNode };

type ToastApi = {
  success: (message: ReactNode) => void;
  error: (err: unknown) => void;
  info: (message: ReactNode) => void;
};

const ToastContext = createContext<ToastApi | null>(null);
let nextId = 1;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastItem[]>([]);

  const dismiss = useCallback((id: number) => setToasts((t) => t.filter((x) => x.id !== id)), []);
  const push = useCallback(
    (kind: ToastKind, message: ReactNode) => {
      const id = nextId++;
      setToasts((t) => [...t.slice(-3), { id, kind, message }]);
      window.setTimeout(() => dismiss(id), kind === "error" ? 7000 : 3500);
    },
    [dismiss],
  );

  const api = useMemo<ToastApi>(
    () => ({
      success: (m) => push("success", m),
      error: (err) => push("error", typeof err === "string" ? err : errorMessage(err)),
      info: (m) => push("info", m),
    }),
    [push],
  );

  return (
    <ToastContext.Provider value={api}>
      {children}
      <div className="pointer-events-none fixed inset-x-0 bottom-20 z-[60] flex flex-col items-center gap-2 px-4 md:bottom-4 md:items-end">
        {toasts.map((t) => (
          <div
            key={t.id}
            role={t.kind === "error" ? "alert" : "status"}
            className="animate-up pointer-events-auto flex w-full max-w-sm items-start gap-2.5 rounded-lg border border-line-strong bg-surface px-3 py-2.5 text-sm shadow-pop"
          >
            {t.kind === "success" ? (
              <CircleCheck className="mt-0.5 size-4 shrink-0 text-good" aria-hidden />
            ) : t.kind === "error" ? (
              <CircleAlert className="mt-0.5 size-4 shrink-0 text-critical" aria-hidden />
            ) : (
              <Info className="mt-0.5 size-4 shrink-0 text-accent" aria-hidden />
            )}
            <div className={clsx("min-w-0 flex-1 break-words")}>{t.message}</div>
            <button
              type="button"
              onClick={() => dismiss(t.id)}
              className="-m-0.5 rounded p-0.5 text-fg-3 hover:text-fg"
              aria-label="Dismiss"
            >
              <X className="size-3.5" />
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast(): ToastApi {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error("useToast outside ToastProvider");
  return ctx;
}
