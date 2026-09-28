import { Copy, Laptop, LogOut, ShieldCheck, ShieldOff } from "lucide-react";
import QRCode from "qrcode";
import { useEffect, useMemo, useState } from "react";
import { api, ApiError } from "@/api/client";
import {
  useChangePassword,
  useRevokeSession,
  useSessions,
  useTotpDisable,
  useTotpEnable,
  useUpdateMe,
} from "@/api/hooks";
import { CodeInput } from "@/components/ui/CodeInput";
import { Switch } from "@/components/ui/Input";
import { Badge } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { Field, Input, Select } from "@/components/ui/Input";
import { PageHeader, Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { Segmented } from "@/components/ui/Tabs";
import { useToast } from "@/components/ui/Toast";
import { useUser } from "@/lib/auth";
import { passwordStrength } from "@/lib/password";
import { useTheme, type ThemePref } from "@/lib/theme";
import { describeAgent } from "@/lib/security";
import { useHashScroll } from "@/lib/docs";
import { timezoneOptions } from "@/lib/setup";
import { groupSecret } from "@/lib/vault";
import { AccentPicker } from "./AccentPicker";
import { RevokeOthersButton, SecurityLogPanel } from "./SecurityLog";

export function SettingsPage() {
  useHashScroll();
  return (
    <div className="mx-auto max-w-3xl space-y-4">
      <PageHeader title="Settings" />
      <AccountPanel />
      <PasswordPanel />
      <TwoFactorPanel />
      <CheckupPanel />
      <AppearancePanel />
      <section id="security" aria-labelledby="settings-security-h" className="scroll-mt-16 space-y-4 pt-2">
        <h2 id="settings-security-h" className="text-[15px] font-semibold tracking-tight">
          Security
        </h2>
        <SessionsPanel />
        <SecurityLogPanel />
      </section>
    </div>
  );
}

function AccountPanel() {
  const user = useUser();
  const update = useUpdateMe();
  const toast = useToast();
  const [displayName, setDisplayName] = useState(user.display_name);
  const [email, setEmail] = useState(user.email);
  const [timezone, setTimezone] = useState(user.timezone);
  const [goal, setGoal] = useState(user.weekly_goal);
  const zones = useMemo(() => timezoneOptions(user.timezone), [user.timezone]);
  const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const dirty =
    displayName !== user.display_name || email !== user.email || timezone !== user.timezone || goal !== user.weekly_goal;

  return (
    <Panel title="Account" id="settings-account" bodyClassName="p-4">
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          update.mutate(
            { display_name: displayName.trim(), email: email.trim(), timezone, weekly_goal: goal },
            { onSuccess: () => toast.success("Account saved"), onError: (err) => toast.error(err) },
          );
        }}
      >
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="Username" hint="Fixed — sign in with it or with your e-mail">
            {(id, desc) => <Input id={id} aria-describedby={desc} value={user.username} disabled />}
          </Field>
          <Field label="Display name">
            {(id) => <Input id={id} value={displayName} onChange={(e) => setDisplayName(e.target.value)} maxLength={80} />}
          </Field>
          <Field label="E-mail" hint="Where password reset links go">
            {(id, desc) => (
              <Input id={id} aria-describedby={desc} type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
            )}
          </Field>
          <Field label="Weekly goal" hint="Tasks to finish per week">
            {(id, desc) => (
              <Input
                id={id}
                aria-describedby={desc}
                type="number"
                min={1}
                max={500}
                value={goal}
                onChange={(e) => setGoal(Math.max(1, Number(e.target.value) || 1))}
              />
            )}
          </Field>
          <Field
            label="Timezone"
            className="sm:col-span-2"
            hint={
              browserZone && browserZone !== timezone ? (
                <>
                  This device is in {browserZone}.{" "}
                  <button type="button" className="text-accent hover:underline" onClick={() => setTimezone(browserZone)}>
                    Use it
                  </button>
                </>
              ) : (
                "Decides what “today” is for streaks, due dates and the weekly goal"
              )
            }
          >
            {(id, desc) => (
              <Select id={id} aria-describedby={desc} value={timezone} onChange={(e) => setTimezone(e.target.value)}>
                {zones.map((z) => (
                  <option key={z} value={z}>
                    {z}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        </div>
        <div className="flex justify-end">
          <Button type="submit" variant="primary" loading={update.isPending} disabled={!dirty}>
            Save
          </Button>
        </div>
      </form>
    </Panel>
  );
}

function PasswordPanel() {
  const user = useUser();
  const change = useChangePassword();
  const toast = useToast();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const strength = passwordStrength(next);
  const mismatch = confirm.length > 0 && confirm !== next;
  const tooShort = next.length > 0 && next.length < 12;

  return (
    <Panel title="Password" id="settings-password" bodyClassName="p-4">
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          if (tooShort || mismatch) return;
          change.mutate(
            { current_password: current, new_password: next },
            {
              onSuccess: () => {
                toast.success("Password changed — other sessions were signed out");
                setCurrent("");
                setNext("");
                setConfirm("");
              },
              onError: (err) => toast.error(err),
            },
          );
        }}
      >
        {/* Lets a password manager file the new password under the right account. */}
        <input type="text" autoComplete="username" value={user.username} className="hidden" readOnly aria-hidden tabIndex={-1} />
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <Field label="Current password">
            {(id) => (
              <Input id={id} type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} required />
            )}
          </Field>
          <Field
            label="New password"
            error={tooShort ? "At least 12 characters" : undefined}
            hint={next ? <StrengthBar score={strength.score} label={strength.label} /> : "At least 12 characters"}
          >
            {(id, desc) => (
              <Input
                id={id}
                aria-describedby={desc}
                aria-invalid={tooShort}
                type="password"
                autoComplete="new-password"
                value={next}
                onChange={(e) => setNext(e.target.value)}
                required
              />
            )}
          </Field>
          <Field label="Confirm" error={mismatch ? "Doesn't match" : undefined}>
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
        </div>
        <div className="flex justify-end">
          <Button type="submit" variant="primary" loading={change.isPending} disabled={!current || next.length < 12 || next !== confirm}>
            Change password
          </Button>
        </div>
      </form>
    </Panel>
  );
}

function StrengthBar({ score, label }: { score: number; label: string }) {
  return (
    <span className="flex items-center gap-2">
      <span className="flex gap-0.5" aria-hidden>
        {[1, 2, 3, 4].map((i) => (
          <span key={i} className={`h-1 w-5 rounded-full ${i <= score ? (score >= 3 ? "bg-good" : "bg-warning") : "bg-surface-3"}`} />
        ))}
      </span>
      {label}
    </span>
  );
}

function AppearancePanel() {
  const { pref, setPref } = useTheme();
  return (
    <Panel title="Appearance" id="settings-appearance" bodyClassName="space-y-5 p-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-[13px] text-fg-2">Theme for this browser.</p>
        <Segmented<ThemePref>
          label="Theme"
          value={pref}
          onChange={setPref}
          items={[
            { value: "system", label: "System" },
            { value: "light", label: "Light" },
            { value: "dark", label: "Dark" },
          ]}
        />
      </div>
      <AccentPicker />
    </Panel>
  );
}

function SessionsPanel() {
  const sessions = useSessions();
  const revoke = useRevokeSession();
  const toast = useToast();
  return (
    <Panel
      title="Sessions"
      id="settings-sessions"
      bodyClassName="p-0"
      actions={<RevokeOthersButton count={(sessions.data ?? []).filter((x) => !x.current).length} />}
    >
      {sessions.isPending ? (
        <SkeletonRows rows={2} className="p-3" />
      ) : (
        <ul className="divide-y divide-line">
          {sessions.data?.map((s) => (
            <li key={s.id} className="flex flex-wrap items-center gap-3 px-4 py-3">
              <Laptop className="size-4 text-fg-3" aria-hidden />
              <div className="min-w-0 flex-1">
                <p className="flex items-center gap-2 text-[13px] font-medium">
                  {describeAgent(s.user_agent)} {s.current ? <Badge tone="accent">this device</Badge> : null}
                </p>
                <p className="truncate text-[11.5px] text-fg-3">
                  {s.ip || "unknown IP"} · active <RelativeTime iso={s.last_seen_at} /> · signed in <RelativeTime iso={s.created_at} />
                </p>
              </div>
              {!s.current ? (
                <Button
                  size="sm"
                  variant="subtle"
                  loading={revoke.isPending && revoke.variables === s.id}
                  onClick={() =>
                    revoke.mutate(s.id, {
                      onSuccess: () => toast.success("Session signed out"),
                      onError: (e) => toast.error(e),
                    })
                  }
                >
                  <LogOut className="size-3.5" aria-hidden /> Sign out
                </Button>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}

function TwoFactorPanel() {
  const user = useUser();
  return (
    <Panel
      title={
        <span className="flex items-center gap-2">
          Two-factor authentication
          {user.totp_enabled ? (
            <Badge tone="good" dot>
              on
            </Badge>
          ) : (
            <Badge tone="outline">off</Badge>
          )}
        </span>
      }
      id="settings-2fa"
      bodyClassName="p-4"
    >
      {user.totp_enabled ? <DisableTotp /> : <SetupTotp />}
    </Panel>
  );
}

function SetupTotp() {
  const toast = useToast();
  const enable = useTotpEnable();
  // The pending secret lives in this component only — never in the query cache.
  const [setup, setSetup] = useState<{ secret: string; otpauth_url: string } | null>(null);
  const [qr, setQr] = useState<string | null>(null);
  const [starting, setStarting] = useState(false);
  const [code, setCode] = useState("");

  useEffect(() => {
    if (!setup) return;
    let cancelled = false;
    // SVG rather than a canvas PNG: pure JS, crisp at any size, and allowed by
    // the CSP as a data: image.
    QRCode.toString(setup.otpauth_url, { type: "svg", margin: 1, errorCorrectionLevel: "M", color: { dark: "#000000", light: "#ffffff" } }).then(
      (svg) => !cancelled && setQr(`data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`),
      () => !cancelled && setQr(null),
    );
    return () => {
      cancelled = true;
    };
  }, [setup]);

  const start = async () => {
    setStarting(true);
    try {
      setSetup(await api.post<{ secret: string; otpauth_url: string }>("/auth/totp/setup"));
    } catch (err) {
      toast.error(err);
    } finally {
      setStarting(false);
    }
  };

  if (!setup) {
    return (
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="max-w-md text-[13px] text-fg-2">
          Ask for a code from an authenticator app (1Password, Aegis, Google Authenticator…) at sign-in and before
          sensitive actions like revealing a vault secret.
        </p>
        <Button variant="primary" onClick={start} loading={starting}>
          <ShieldCheck className="size-4" aria-hidden /> Set up
        </Button>
      </div>
    );
  }

  const wrongCode = enable.error instanceof ApiError && (enable.error.status === 401 || enable.error.status === 400 || enable.error.status === 422);

  return (
    <div className="grid gap-5 sm:grid-cols-[auto_1fr]">
      <div className="justify-self-center rounded-lg bg-white p-2 sm:justify-self-start">
        {qr ? (
          <img src={qr} alt="QR code for your authenticator app" width={220} height={220} className="block" />
        ) : (
          <div className="size-[220px]" />
        )}
      </div>
      <div className="space-y-3 text-[13px]">
        <ol className="list-decimal space-y-1 pl-4 text-fg-2">
          <li>Scan the code with your authenticator app, or enter the key below by hand.</li>
          <li>Type the 6-digit code it shows to switch two-factor on.</li>
        </ol>
        <div>
          <div className="mb-1 text-fg-3">Setup key</div>
          <div className="flex items-center gap-2">
            <code className="rounded-md border border-line bg-surface-2 px-2 py-1 font-mono text-[13px] break-all">
              {groupSecret(setup.secret)}
            </code>
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label="Copy setup key"
              onClick={() => void navigator.clipboard?.writeText(setup.secret).then(() => toast.success("Key copied"))}
            >
              <Copy className="size-3.5" />
            </Button>
          </div>
        </div>
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (code.length !== 6) return;
            enable.mutate(code, {
              onSuccess: () => {
                toast.success("Two-factor authentication is on");
                setSetup(null);
                setQr(null);
              },
            });
          }}
        >
          <Field label="Code" className="w-40" error={wrongCode ? "That code didn't match — try the next one" : undefined}>
            {(id, desc) => <CodeInput id={id} aria-describedby={desc} value={code} onChange={setCode} aria-invalid={wrongCode} />}
          </Field>
          <Button type="submit" variant="primary" loading={enable.isPending} disabled={code.length !== 6}>
            Turn on
          </Button>
          <Button variant="ghost" onClick={() => setSetup(null)}>
            Cancel
          </Button>
        </form>
      </div>
    </div>
  );
}

function DisableTotp() {
  const user = useUser();
  const disable = useTotpDisable();
  const toast = useToast();
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const wrong = disable.error instanceof ApiError && disable.error.status === 401;
  return (
    <form
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (!password || code.length !== 6) return;
        disable.mutate(
          { password, code },
          {
            onSuccess: () => {
              toast.success("Two-factor authentication is off");
              setPassword("");
              setCode("");
            },
            onError: (err) => !(err instanceof ApiError && err.status === 401) && toast.error(err),
          },
        );
      }}
    >
      <p className="text-[13px] text-fg-2">
        Sign-in and sensitive actions ask for a code from your authenticator app. To turn it off, confirm with your
        password and a current code.
      </p>
      <input type="text" autoComplete="username" value={user.username} className="hidden" readOnly aria-hidden tabIndex={-1} />
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_10rem_auto] sm:items-end">
        <Field label="Password" error={wrong ? "Wrong password or code" : undefined}>
          {(id, desc) => (
            <Input
              id={id}
              aria-describedby={desc}
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          )}
        </Field>
        <Field label="Code">{(id) => <CodeInput id={id} value={code} onChange={setCode} className="h-8.5 text-base" />}</Field>
        <Button type="submit" variant="subtle" loading={disable.isPending} disabled={!password || code.length !== 6}>
          <ShieldOff className="size-3.5" aria-hidden /> Turn off
        </Button>
      </div>
    </form>
  );
}

function CheckupPanel() {
  const user = useUser();
  const update = useUpdateMe();
  const toast = useToast();
  const [time, setTime] = useState(user.checkup_time || "08:00");
  const [email, setEmail] = useState(user.checkup_email);
  const dirty = time !== user.checkup_time || email !== user.checkup_email;
  return (
    <Panel title="Daily check-up" id="settings-checkup" bodyClassName="p-4">
      <form
        className="flex flex-wrap items-end gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          update.mutate(
            { checkup_time: time, checkup_email: email },
            { onSuccess: () => toast.success("Check-up preferences saved"), onError: (err) => toast.error(err) },
          );
        }}
      >
        <Field label="Runs daily at" hint={`${user.timezone} time`} className="w-40">
          {(id, desc) => (
            <Input id={id} aria-describedby={desc} type="time" value={time} onChange={(e) => setTime(e.target.value)} required />
          )}
        </Field>
        <div className="pb-6">
          <Switch checked={email} onChange={setEmail} label={`E-mail the result to ${user.email}`} />
        </div>
        <Button type="submit" variant="primary" className="mb-6 ml-auto" loading={update.isPending} disabled={!dirty || !/^\d{2}:\d{2}$/.test(time)}>
          Save
        </Button>
      </form>
    </Panel>
  );
}
