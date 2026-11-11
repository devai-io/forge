// Sign-in, forgot-password and reset-password: three small centred cards
// (first-run setup, in SetupPage.tsx, uses the same card).
//
// Sign-in leads with a passkey (it signs in on its own). The password is the
// fallback and always needs the authenticator code too; an account without
// an authenticator sets one up right there.
//
// The forgot form always answers the same way, whether or not the login
// exists — that is the API's contract, and the copy here does not undo it.

import { KeyRound } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link, Navigate, useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { ApiError } from "@/api/client";
import { useForgotPassword, useLogin, usePasskeyLogin, useResetPassword, useSetupStatus } from "@/api/hooks";
import { Button } from "@/components/ui/Button";
import { CodeInput } from "@/components/ui/CodeInput";
import { Field, Input } from "@/components/ui/Input";
import { FullPageSpinner } from "@/components/ui/Spinner";
import { safeNext, useCurrentUser } from "@/lib/auth";
import { passwordStrength } from "@/lib/password";
import { setupRedirect } from "@/lib/setup";
import { isPasskeyCancel, passkeysSupported } from "@/lib/webauthn";
import { SetupKey, TotpQrCode } from "./TotpQr";

export function AuthCard({
  title,
  subtitle,
  children,
  wide,
}: {
  title: string;
  subtitle?: ReactNode;
  children: ReactNode;
  wide?: boolean;
}) {
  return (
    <div className="grid min-h-dvh place-items-center bg-bg px-4 py-10">
      <div className={wide ? "w-full max-w-lg" : "w-full max-w-sm"}>
        <div className="mb-6 flex items-center justify-center gap-2 text-lg font-semibold tracking-tight">
          <img src="/favicon.svg" alt="" className="size-7" />
          Forge
        </div>
        <div className="rounded-xl border border-line-strong bg-surface p-6 shadow-pop">
          <h1 className="text-[17px] font-semibold">{title}</h1>
          {subtitle ? <p className="mt-1 text-[13px] text-fg-3">{subtitle}</p> : null}
          <div className="mt-5">{children}</div>
        </div>
      </div>
    </div>
  );
}

export function FormError({ children }: { children: ReactNode }) {
  if (!children) return null;
  return (
    <p role="alert" className="rounded-md border border-critical/40 bg-critical/8 px-3 py-2 text-[13px]">
      {children}
    </p>
  );
}

type TotpSetup = { secret: string; otpauth_url: string };

export function LoginPage() {
  const { user, loading } = useCurrentUser();
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const login = useLogin();
  const passkey = usePasskeyLogin();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  // The password was right: now the code (2FA on), or setting up an
  // authenticator first (an account without one). Stays for the retry.
  const [step, setStep] = useState<"password" | "code" | "setup">("password");
  const [setup, setSetup] = useState<TotpSetup | null>(null);
  const next = safeNext(params.get("next"));
  // A server without an account has nobody to sign in: first-run setup instead.
  const setupStatus = useSetupStatus();
  const location = useLocation();
  const canPasskey = passkeysSupported();

  if (loading || setupStatus.isPending) return <FullPageSpinner />;
  if (user) return <Navigate to={next} replace />;
  const toSetup = setupRedirect(setupStatus.data?.needed, location.search);
  if (toSetup) return <Navigate to={toSetup} replace />;

  const err = login.error instanceof ApiError ? login.error : null;
  const error = err
    ? err.code === "totp_required" || err.code === "totp_setup_required"
      ? null // not a failure: the next step has just appeared
      : err.code === "rate_limited"
        ? "Too many attempts. Wait a few minutes and try again."
        : err.status === 401
          ? step === "password"
            ? "Wrong username or password."
            : "Wrong code — try the next one."
          : err.message
    : login.error
      ? "Sign-in failed."
      : null;
  const passkeyError =
    passkey.error && !isPasskeyCancel(passkey.error)
      ? passkey.error instanceof ApiError
        ? passkey.error.code === "rate_limited"
          ? "Too many attempts. Wait a few minutes and try again."
          : passkey.error.code === "passkeys_unavailable"
            ? "Passkeys are not available on this server. Use your password and code."
            : passkey.error.status === 401
              ? "That passkey was not accepted."
              : passkey.error.message
        : "The passkey could not be used here. Try again, or use your password and code."
      : null;

  const signInWithPasskey = () => passkey.mutate(undefined, { onSuccess: () => navigate(next, { replace: true }) });

  return (
    <AuthCard title="Sign in" subtitle="Your projects, tasks and agents.">
      <div className="space-y-4">
        {canPasskey && step === "password" ? (
          <>
            <FormError>{passkeyError}</FormError>
            <Button type="button" variant="primary" className="w-full justify-center" loading={passkey.isPending} onClick={signInWithPasskey}>
              <KeyRound className="size-4" aria-hidden /> Sign in with a passkey
            </Button>
            <div className="flex items-center gap-3 text-[12px] text-fg-3" aria-hidden>
              <span className="h-px flex-1 bg-line" /> or with your password and code <span className="h-px flex-1 bg-line" />
            </div>
          </>
        ) : null}
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (step !== "password" && code.length !== 6) return;
            login.mutate(
              { username: username.trim(), password, code: step !== "password" ? code : undefined },
              {
                onSuccess: () => navigate(next, { replace: true }),
                onError: (e) => {
                  if (!(e instanceof ApiError)) return;
                  if (e.code === "totp_required") setStep("code");
                  if (e.code === "totp_setup_required") {
                    const d = e.details ?? {};
                    setSetup({ secret: String(d.secret ?? ""), otpauth_url: String(d.otpauth_url ?? "") });
                    setStep("setup");
                  }
                  if (e.code === "totp_required" || e.code === "totp_setup_required") setCode("");
                },
              },
            );
          }}
        >
          <FormError>{error}</FormError>
          <div className={step === "password" ? "space-y-4" : "hidden"}>
            <Field label="Username or e-mail">
              {(id) => (
                <Input
                  id={id}
                  autoFocus={!canPasskey}
                  autoComplete="username"
                  autoCapitalize="none"
                  spellCheck={false}
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  required
                />
              )}
            </Field>
            <Field label="Password">
              {(id) => (
                <Input
                  id={id}
                  type="password"
                  autoComplete="current-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  required
                />
              )}
            </Field>
          </div>
          {step === "setup" && setup ? (
            <div className="space-y-3 text-[13px]">
              <p className="text-fg-2">
                A password sign-in needs a code from an authenticator app (1Password, Aegis, Google Authenticator…).
                Scan this once, then type the code it shows.
              </p>
              <div className="grid justify-items-center">
                <TotpQrCode otpauthUrl={setup.otpauth_url} size={180} centered />
              </div>
              <SetupKey secret={setup.secret} />
            </div>
          ) : null}
          {step !== "password" ? (
            <Field label="Authenticator code" hint="The 6-digit code from your authenticator app">
              {(id, desc) => <CodeInput id={id} aria-describedby={desc} autoFocus value={code} onChange={setCode} required />}
            </Field>
          ) : null}
          <Button
            type="submit"
            variant={canPasskey && step === "password" ? "secondary" : "primary"}
            className="w-full justify-center"
            loading={login.isPending}
            disabled={step !== "password" && code.length !== 6}
          >
            {step === "password" ? "Continue" : step === "setup" ? "Turn on two-factor and sign in" : "Verify and sign in"}
          </Button>
          <p className="flex justify-center gap-4 text-center text-[13px]">
            {step !== "password" ? (
              <button
                type="button"
                className="text-fg-3 hover:text-fg"
                onClick={() => {
                  setStep("password");
                  setCode("");
                  login.reset();
                }}
              >
                Back
              </button>
            ) : null}
            <Link to="/forgot" className="text-fg-3 hover:text-fg">
              Forgot your password?
            </Link>
          </p>
        </form>
      </div>
    </AuthCard>
  );
}

export function ForgotPage() {
  const forgot = useForgotPassword();
  const [login, setLogin] = useState("");
  if (forgot.isSuccess) {
    return (
      <AuthCard title="Check your e-mail">
        <p className="text-[13px] text-fg-2">
          If that account exists, a reset link is on its way. It expires in an hour. If mail is not configured on this
          server, the link is written to the API's log instead.
        </p>
        <Link to="/login" className="mt-5 inline-block text-[13px] text-accent hover:underline">
          Back to sign in
        </Link>
      </AuthCard>
    );
  }
  return (
    <AuthCard title="Reset password" subtitle="We'll send a reset link to the account's e-mail.">
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          forgot.mutate(login.trim());
        }}
      >
        <FormError>{forgot.error ? (forgot.error as Error).message : null}</FormError>
        <Field label="Username or e-mail">
          {(id) => (
            <Input
              id={id}
              autoFocus
              autoComplete="username"
              autoCapitalize="none"
              value={login}
              onChange={(e) => setLogin(e.target.value)}
              required
            />
          )}
        </Field>
        <Button type="submit" variant="primary" className="w-full justify-center" loading={forgot.isPending}>
          Send reset link
        </Button>
        <p className="text-center text-[13px]">
          <Link to="/login" className="text-fg-3 hover:text-fg">
            Back to sign in
          </Link>
        </p>
      </form>
    </AuthCard>
  );
}

export function ResetPage() {
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const reset = useResetPassword();
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const strength = passwordStrength(password);
  const mismatch = confirm.length > 0 && confirm !== password;

  if (!token) {
    return (
      <AuthCard title="Invalid link">
        <p className="text-[13px] text-fg-2">This reset link is missing its token.</p>
        <Link to="/forgot" className="mt-5 inline-block text-[13px] text-accent hover:underline">
          Request a new one
        </Link>
      </AuthCard>
    );
  }
  if (reset.isSuccess) {
    return (
      <AuthCard title="Password changed">
        <p className="text-[13px] text-fg-2">Every other session was signed out. Sign in with the new password.</p>
        <Link to="/login" className="mt-5 inline-block text-[13px] text-accent hover:underline">
          Sign in
        </Link>
      </AuthCard>
    );
  }
  return (
    <AuthCard title="Choose a new password" subtitle="At least 12 characters.">
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          if (password.length < 12 || mismatch) return;
          reset.mutate({ token, new_password: password });
        }}
      >
        <FormError>
          {reset.error instanceof ApiError && reset.error.code === "bad_request"
            ? "This link is invalid or has expired. Request a new one."
            : reset.error
              ? (reset.error as Error).message
              : null}
        </FormError>
        <Field label="New password" hint={password ? `Strength: ${strength.label}` : "At least 12 characters"}>
          {(id, desc) => (
            <Input
              id={id}
              aria-describedby={desc}
              type="password"
              autoComplete="new-password"
              autoFocus
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              minLength={12}
              required
            />
          )}
        </Field>
        <Field label="Confirm password" error={mismatch ? "Passwords don't match" : undefined}>
          {(id, desc) => (
            <Input
              id={id}
              aria-describedby={desc}
              aria-invalid={mismatch}
              type="password"
              autoComplete="new-password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              required
            />
          )}
        </Field>
        <Button
          type="submit"
          variant="primary"
          className="w-full justify-center"
          loading={reset.isPending}
          disabled={password.length < 12 || mismatch || !confirm}
        >
          Set password
        </Button>
      </form>
    </AuthCard>
  );
}
