// "Confirm it's you": the step-up dialog behind every 403 elevation_required.
//
// The API client calls the registered handler, this component shows the
// dialog, and the promise it returned resolves true (retry the request) or
// false (the user cancelled; the original error surfaces). One dialog serves
// any number of requests that were waiting on it.

import { KeyRound, ShieldCheck } from "lucide-react";
import { useEffect, useState } from "react";
import { ApiError, setElevationHandler } from "@/api/client";
import { useElevate, useMe, usePasskeyElevate } from "@/api/hooks";
import { Button } from "@/components/ui/Button";
import { CodeInput } from "@/components/ui/CodeInput";
import { Dialog } from "@/components/ui/Dialog";
import { Field, Input } from "@/components/ui/Input";
import { useCurrentUser, useDeviceTrusted } from "@/lib/auth";
import { isPasskeyCancel, passkeysSupported } from "@/lib/webauthn";

export function ElevationGate() {
  const [resolve, setResolve] = useState<((ok: boolean) => void) | null>(null);

  useEffect(() => {
    setElevationHandler(() => new Promise<boolean>((res) => setResolve(() => res)));
    return () => setElevationHandler(null);
  }, []);

  if (!resolve) return null;
  return (
    <ElevationDialog
      onDone={(ok) => {
        resolve(ok);
        setResolve(null);
      }}
    />
  );
}

function ElevationDialog({ onDone }: { onDone: (ok: boolean) => void }) {
  const { user } = useCurrentUser();
  const trusted = useDeviceTrusted();
  const elevate = useElevate();
  const passkey = usePasskeyElevate();
  // A passkey confirms on its own; the password (and code) stays the fallback.
  const hasPasskey = (useMe().data?.passkeys ?? 0) > 0 && passkeysSupported();
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  // A browser that passed two-factor before is only asked for the password —
  // unless the server has forgotten it since (it then asks for the code).
  const codeAsked = elevate.error instanceof ApiError && elevate.error.field === "code";
  const needsCode = !!user?.totp_enabled && (!trusted || codeAsked);

  const error =
    elevate.error instanceof ApiError
      ? codeAsked && trusted && !code
        ? "This browser needs the authenticator code again."
        : elevate.error.status === 401
        ? needsCode
          ? "Wrong password or code."
          : "Wrong password."
        : elevate.error.code === "rate_limited"
          ? "Too many attempts. Wait a few minutes."
          : elevate.error.message
      : null;

  const passkeyError =
    passkey.error && !isPasskeyCancel(passkey.error)
      ? passkey.error instanceof ApiError && passkey.error.code === "rate_limited"
        ? "Too many attempts. Wait a few minutes."
        : "That passkey was not accepted. Try again, or use your password."
      : null;

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!password || (needsCode && code.length !== 6)) return;
    elevate.mutate({ password, code: needsCode ? code : undefined }, { onSuccess: () => onDone(true) });
  };

  return (
    <Dialog
      open
      onClose={() => onDone(false)}
      size="sm"
      title={
        <span className="flex items-center gap-2">
          <ShieldCheck className="size-4 text-accent" aria-hidden /> Confirm it's you
        </span>
      }
      description={
        hasPasskey
          ? "This action needs your passkey or password again. It stays unlocked for 10 minutes."
          : "This action needs your password again. It stays unlocked for 10 minutes."
      }
      footer={
        <>
          <Button variant="ghost" onClick={() => onDone(false)}>
            Cancel
          </Button>
          <Button
            variant="primary"
            type="submit"
            form="elevate-form"
            loading={elevate.isPending}
            disabled={!password || (needsCode && code.length !== 6)}
          >
            Confirm
          </Button>
        </>
      }
    >
      <form id="elevate-form" onSubmit={submit} className="space-y-3">
        {hasPasskey ? (
          <>
            {passkeyError ? (
              <p role="alert" className="rounded-md border border-critical/40 bg-critical/8 px-3 py-2 text-[13px]">
                {passkeyError}
              </p>
            ) : null}
            <Button
              type="button"
              variant="primary"
              className="w-full justify-center"
              data-autofocus
              loading={passkey.isPending}
              onClick={() => passkey.mutate(undefined, { onSuccess: () => onDone(true) })}
            >
              <KeyRound className="size-4" aria-hidden /> Use a passkey
            </Button>
            <div className="flex items-center gap-3 text-[12px] text-fg-3" aria-hidden>
              <span className="h-px flex-1 bg-line" /> or your password <span className="h-px flex-1 bg-line" />
            </div>
          </>
        ) : null}
        {error ? (
          <p role="alert" className="rounded-md border border-critical/40 bg-critical/8 px-3 py-2 text-[13px]">
            {error}
          </p>
        ) : null}
        <input type="text" autoComplete="username" value={user?.username ?? ""} className="hidden" readOnly aria-hidden tabIndex={-1} />
        <Field label="Password">
          {(id) => (
            <Input
              id={id}
              data-autofocus={hasPasskey ? undefined : true}
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
            />
          )}
        </Field>
        {needsCode ? (
          <Field label="Authenticator code">
            {(id) => <CodeInput id={id} value={code} onChange={setCode} required />}
          </Field>
        ) : null}
      </form>
    </Dialog>
  );
}
