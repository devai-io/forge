import clsx from "clsx";
import { Laptop, LogOut, ShieldCheck, ShieldOff } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { api, ApiError } from "@/api/client";
import {
  useChangePassword,
  useForgetTrustedDevices,
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
import { useDeviceTrusted, useUser } from "@/lib/auth";
import { passwordStrength } from "@/lib/password";
import { useTheme, type ThemePref } from "@/lib/theme";
import { describeAgent } from "@/lib/security";
import { useHashScroll } from "@/lib/docs";
import { timezoneOptions } from "@/lib/setup";
import { AccentPicker } from "./AccentPicker";
import { AssistantPanel } from "./AssistantPanel";
import { EndpointsPanel } from "./EndpointsPanel";
import { EnginePanel } from "./EnginePanel";
import { ProviderPanel } from "./ProviderPanel";
import { TokenSavingPanel } from "./TokenSaving";
import { TelegramPanel } from "./TelegramPanel";
import { MemoryPanel } from "@/features/memory/MemoryPanel";
import { RevokeOthersButton, SecurityLogPanel } from "./SecurityLog";
import { PasskeysPanel } from "./PasskeysPanel";
import { SetupKey, TotpQrCode } from "@/features/auth/TotpQr";

// The page's groups, in page order, for the "On this page" nav. Ids are the
// panels' own heading ids (Panel puts `id` on its <h2>), so old deep links
// like /settings#settings-2fa keep working.
const GROUPS: { id: string; title: string; panels: { id: string; title: string }[] }[] = [
  {
    id: "account",
    title: "Account",
    panels: [
      { id: "settings-account", title: "Account" },
      { id: "settings-passkeys", title: "Passkeys" },
      { id: "settings-password", title: "Password" },
      { id: "settings-2fa", title: "Two-factor" },
    ],
  },
  {
    id: "operations",
    title: "Operations",
    panels: [
      { id: "settings-checkup", title: "Daily check-up" },
      { id: "settings-endpoints", title: "Endpoint checks" },
      { id: "settings-telegram", title: "Telegram" },
    ],
  },
  {
    id: "ai",
    title: "AI",
    panels: [
      { id: "settings-provider", title: "AI provider" },
      { id: "settings-engine", title: "Agent engine" },
      { id: "settings-jev", title: "Token saving" },
      { id: "settings-assistant", title: "Assistant" },
      { id: "settings-memory", title: "Memory" },
    ],
  },
  { id: "appearance", title: "Appearance", panels: [{ id: "settings-appearance", title: "Appearance" }] },
  {
    id: "security",
    title: "Security",
    panels: [
      { id: "settings-sessions", title: "Sessions" },
      { id: "settings-security-log", title: "Security log" },
    ],
  },
];

/** Which panel heading is at the top of the viewport (the DocsPage pattern). */
function useActivePanel(): string {
  const [active, setActive] = useState<string>(GROUPS[0].panels[0].id);
  useEffect(() => {
    if (typeof IntersectionObserver === "undefined") return;
    const io = new IntersectionObserver(
      (entries) => {
        const visible = entries.filter((e) => e.isIntersecting).sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top);
        if (visible[0]) setActive(visible[0].target.id);
      },
      { rootMargin: "-80px 0px -70% 0px" },
    );
    for (const g of GROUPS) {
      for (const p of g.panels) {
        const el = document.getElementById(p.id);
        if (el) io.observe(el);
      }
    }
    return () => io.disconnect();
  }, []);
  return active;
}

function SettingsNav() {
  const active = useActivePanel();
  const activeGroup = GROUPS.find((g) => g.panels.some((p) => p.id === active))?.id;
  return (
    <nav aria-label="On this page" className="mb-4 lg:sticky lg:top-16 lg:mb-0">
      <p className="mb-2 hidden text-[11px] font-semibold tracking-wide text-fg-3 uppercase lg:block">On this page</p>
      {/* Phones and tablets: the groups on one row that scrolls sideways. */}
      <ol className="-mx-4 flex gap-1.5 overflow-x-auto px-4 pb-1 lg:hidden">
        {GROUPS.map((g) => (
          <li key={g.id} className="shrink-0">
            <a
              href={`#${g.id === "security" ? "security" : g.id === "appearance" ? "settings-appearance" : `settings-group-${g.id}`}`}
              aria-current={activeGroup === g.id ? "location" : undefined}
              className={clsx(
                "block rounded-full border px-3 py-1 text-[12.5px] whitespace-nowrap",
                activeGroup === g.id ? "border-accent/40 bg-accent/10 font-medium text-fg" : "border-line text-fg-2 hover:text-fg",
              )}
            >
              {g.title}
            </a>
          </li>
        ))}
      </ol>
      <div className="hidden space-y-3 lg:block">
        {GROUPS.map((g) => (
          <div key={g.id}>
            <p className="px-2 text-[11.5px] font-semibold text-fg-2">{g.title}</p>
            <ol className="mt-0.5 space-y-0.5">
              {g.panels.map((p) => (
                <li key={p.id}>
                  <a
                    href={`#${p.id}`}
                    aria-current={active === p.id ? "location" : undefined}
                    className={clsx(
                      "block rounded-md px-2 py-1 text-[13px]",
                      active === p.id ? "bg-surface-2 font-medium text-fg" : "text-fg-3 hover:text-fg-2",
                    )}
                  >
                    {p.title}
                  </a>
                </li>
              ))}
            </ol>
          </div>
        ))}
      </div>
    </nav>
  );
}

function GroupHeading({ id, children }: { id: string; children: string }) {
  return (
    <h2 id={id} className="scroll-mt-20 pt-2 text-[15px] font-semibold tracking-tight">
      {children}
    </h2>
  );
}

export function SettingsPage() {
  useHashScroll();
  return (
    <div className="mx-auto max-w-[1000px] lg:grid lg:grid-cols-[180px_minmax(0,1fr)] lg:gap-8">
      <div className="lg:col-start-2 lg:row-start-1">
        <PageHeader title="Settings" />
      </div>
      <div className="lg:col-start-1 lg:row-start-2">
        <SettingsNav />
      </div>
      {/* Panel headings carry the ids; keep them clear of the sticky top bar when jumped to. */}
      <div className="max-w-3xl min-w-0 space-y-4 lg:col-start-2 lg:row-start-2 [&_h2[id]]:scroll-mt-20">
        <section aria-labelledby="settings-group-account" className="space-y-4">
          <GroupHeading id="settings-group-account">Account</GroupHeading>
          <AccountPanel />
          <PasskeysPanel />
          <PasswordPanel />
          <TwoFactorPanel />
        </section>
        <section aria-labelledby="settings-group-operations" className="space-y-4">
          <GroupHeading id="settings-group-operations">Operations</GroupHeading>
          <CheckupPanel />
          <EndpointsPanel />
          <TelegramPanel />
        </section>
        <section aria-labelledby="settings-group-ai" className="space-y-4">
          <GroupHeading id="settings-group-ai">AI</GroupHeading>
          <ProviderPanel />
          <EnginePanel />
          <TokenSavingPanel />
          <AssistantPanel />
          {/* MemoryPanel has no id of its own; this is the nav's anchor for it. */}
          <div id="settings-memory" className="scroll-mt-20">
            <MemoryPanel
              project="global"
              title="Memory about you"
              intro="What Forge remembers about you in every project — preferences, how you like things done. Claude sessions and the Assistant start with these; each project's own memory is on its page."
            />
          </div>
        </section>
        {/* One panel: its own title is the group heading. */}
        <div className="pt-2">
          <AppearancePanel />
        </div>
        <section id="security" aria-labelledby="settings-security-h" className="scroll-mt-16 space-y-4">
          <GroupHeading id="settings-security-h">Security</GroupHeading>
          <SessionsPanel />
          <SecurityLogPanel />
        </section>
      </div>
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
            <Badge tone="warning" dot title="The next sign-in with the password will ask you to set it up">
              off
            </Badge>
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
  const [starting, setStarting] = useState(false);
  const [code, setCode] = useState("");

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
          A password sign-in needs a code from an authenticator app (1Password, Aegis, Google Authenticator…). Set it
          up here, or at your next sign-in with the password.
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
      <TotpQrCode otpauthUrl={setup.otpauth_url} />
      <div className="space-y-3 text-[13px]">
        <ol className="list-decimal space-y-1 pl-4 text-fg-2">
          <li>Scan the code with your authenticator app, or enter the key below by hand.</li>
          <li>Type the 6-digit code it shows to switch two-factor on.</li>
        </ol>
        <SetupKey secret={setup.secret} />
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (code.length !== 6) return;
            enable.mutate(code, {
              onSuccess: () => {
                toast.success("Two-factor authentication is on");
                setSetup(null);
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
        Every sign-in with the password asks for a code from your authenticator app (a passkey does not). To confirm a
        sensitive action, a browser that has typed a code before is trusted and the password is enough. Turning
        two-factor off (password and a current code) means the next password sign-in sets it up again.
      </p>
      <TrustedDevices />
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

/** Whether this browser skips the code, and a way to make every browser ask again. */
function TrustedDevices() {
  const trusted = useDeviceTrusted();
  const forget = useForgetTrustedDevices();
  const toast = useToast();
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-line px-3 py-2 text-[13px]">
      <span className="flex items-center gap-2 text-fg-2">
        <Laptop className="size-3.5 text-fg-3" aria-hidden />
        {trusted
          ? "This browser is trusted: confirming an action needs no code here."
          : "This browser will be asked for the code to confirm actions."}
      </span>
      <Button
        size="sm"
        variant="ghost"
        loading={forget.isPending}
        onClick={() =>
          forget.mutate(undefined, {
            onSuccess: ({ forgotten }) =>
              toast.success(`Every browser asks for the code again (${forgotten} forgotten)`),
            onError: (e) => toast.error(e),
          })
        }
      >
        Ask for the code everywhere
      </Button>
    </div>
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
