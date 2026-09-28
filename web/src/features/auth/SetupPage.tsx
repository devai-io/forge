// First run: the server has no account yet, and whoever holds the setup token
// (printed in the server log, or by `forge setup-token`) creates it here. The
// link in the log carries the token as ?token=, so the field stays hidden
// unless the token is missing or the API turns it down.

import { useMemo, useState } from "react";
import { Navigate, useNavigate, useSearchParams } from "react-router-dom";
import { useSetup, useSetupStatus } from "@/api/hooks";
import { Button } from "@/components/ui/Button";
import { Checkbox, Field, Input, Select } from "@/components/ui/Input";
import { FullPageSpinner } from "@/components/ui/Spinner";
import { passwordStrength } from "@/lib/password";
import {
  browserTimezone,
  PASSWORD_MIN,
  setupFailure,
  timezoneOptions,
  validateSetup,
  type SetupField,
  type SetupForm,
} from "@/lib/setup";
import { AuthCard, FormError } from "./AuthPages";

export function SetupPage() {
  const status = useSetupStatus();
  if (status.isPending) return <FullPageSpinner />;
  // Already set up (or an API that has no setup route): sign in instead.
  if (status.data && !status.data.needed) return <Navigate to="/login" replace />;
  if (status.error) {
    return (
      <AuthCard title="Can't reach Forge">
        <p className="text-[13px] text-fg-2">The server did not answer. Check that it is running, then try again.</p>
        <Button className="mt-5" onClick={() => status.refetch()}>
          Try again
        </Button>
      </AuthCard>
    );
  }
  return <SetupForm />;
}

function SetupForm() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const setup = useSetup();
  const linkToken = params.get("token") ?? "";
  const [form, setForm] = useState<SetupForm>(() => ({
    token: linkToken,
    username: "",
    email: "",
    password: "",
    confirm: "",
    display_name: "",
    timezone: browserTimezone(),
  }));
  const [demo, setDemo] = useState(false);
  const [touched, setTouched] = useState(false);
  const [serverError, setServerError] = useState<{ field?: SetupField; message: string } | null>(null);
  const zones = useMemo(() => timezoneOptions(form.timezone), [form.timezone]);
  const set = (k: keyof SetupForm, v: string) => {
    setForm((f) => ({ ...f, [k]: v }));
    if (serverError?.field === k) setServerError(null);
  };

  const errors = validateSetup(form);
  // Problems show after the first submit attempt — except a mismatched
  // confirmation, which is worth saying while it is being typed.
  const errorFor = (k: SetupField): string | undefined => {
    if (serverError?.field === k) return serverError.message;
    return touched || (k === "confirm" && form.confirm.length > 0) ? errors[k] : undefined;
  };
  const showToken = !linkToken || serverError?.field === "token";
  const strength = passwordStrength(form.password);

  return (
    <AuthCard wide title="Welcome to Forge" subtitle="Create the account that owns this server. There is only ever one.">
      <form
        className="space-y-4"
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          setTouched(true);
          if (Object.keys(errors).length) return;
          setServerError(null);
          setup.mutate(
            {
              token: form.token.trim(),
              username: form.username.trim(),
              email: form.email.trim(),
              password: form.password,
              display_name: form.display_name.trim() || undefined,
              timezone: form.timezone,
              demo_data: demo,
            },
            {
              onSuccess: () => navigate("/", { replace: true }),
              onError: (err) => {
                const f = setupFailure(err);
                if (f.kind === "done") navigate("/login", { replace: true });
                else setServerError(f.kind === "field" ? { field: f.field, message: f.message } : { message: f.message });
              },
            },
          );
        }}
      >
        <FormError>{serverError && !serverError.field ? serverError.message : null}</FormError>
        {showToken ? (
          <Field
            label="Setup token"
            error={errorFor("token")}
            hint={
              <>
                Printed in the server log on first start, or run <code className="font-mono">forge setup-token</code> on
                the server (Docker: <code className="font-mono">docker compose exec forge forge setup-token</code>).
              </>
            }
          >
            {(id, desc) => (
              <Input
                id={id}
                aria-describedby={desc}
                aria-invalid={!!errorFor("token")}
                autoComplete="off"
                autoCapitalize="none"
                spellCheck={false}
                className="font-mono"
                value={form.token}
                onChange={(e) => set("token", e.target.value)}
                autoFocus
              />
            )}
          </Field>
        ) : null}
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field label="Your name" hint="Optional — how Forge greets you" error={errorFor("display_name")}>
            {(id, desc) => (
              <Input
                id={id}
                aria-describedby={desc}
                aria-invalid={!!errorFor("display_name")}
                autoComplete="name"
                value={form.display_name}
                onChange={(e) => set("display_name", e.target.value)}
                maxLength={100}
                autoFocus={!showToken}
              />
            )}
          </Field>
          <Field label="Username" error={errorFor("username")}>
            {(id, desc) => (
              <Input
                id={id}
                aria-describedby={desc}
                aria-invalid={!!errorFor("username")}
                autoComplete="username"
                autoCapitalize="none"
                spellCheck={false}
                value={form.username}
                onChange={(e) => set("username", e.target.value)}
                required
              />
            )}
          </Field>
          <Field label="E-mail" className="sm:col-span-2" error={errorFor("email")} hint="Where password reset links go">
            {(id, desc) => (
              <Input
                id={id}
                aria-describedby={desc}
                aria-invalid={!!errorFor("email")}
                type="email"
                autoComplete="email"
                value={form.email}
                onChange={(e) => set("email", e.target.value)}
                required
              />
            )}
          </Field>
          <Field
            label="Password"
            error={errorFor("password")}
            hint={form.password ? `Strength: ${strength.label}` : `At least ${PASSWORD_MIN} characters`}
          >
            {(id, desc) => (
              <Input
                id={id}
                aria-describedby={desc}
                aria-invalid={!!errorFor("password")}
                type="password"
                autoComplete="new-password"
                value={form.password}
                onChange={(e) => set("password", e.target.value)}
                required
              />
            )}
          </Field>
          <Field label="Confirm password" error={errorFor("confirm")}>
            {(id, desc) => (
              <Input
                id={id}
                aria-describedby={desc}
                aria-invalid={!!errorFor("confirm")}
                type="password"
                autoComplete="new-password"
                value={form.confirm}
                onChange={(e) => set("confirm", e.target.value)}
                required
              />
            )}
          </Field>
          <Field
            label="Timezone"
            className="sm:col-span-2"
            error={errorFor("timezone")}
            hint="Decides what “today” is for streaks, due dates and the daily check-up"
          >
            {(id, desc) => (
              <Select id={id} aria-describedby={desc} value={form.timezone} onChange={(e) => set("timezone", e.target.value)}>
                {zones.map((z) => (
                  <option key={z} value={z}>
                    {z}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        </div>
        <label className="flex items-start gap-2.5 rounded-lg border border-line px-3 py-2.5 text-[13px]">
          <Checkbox className="mt-0.5" checked={demo} onChange={(e) => setDemo(e.target.checked)} />
          <span>
            <span className="font-medium text-fg">Load demo data</span>
            <span className="block text-fg-3">Sample projects, servers and tasks to explore (delete them any time).</span>
          </span>
        </label>
        <Button type="submit" variant="primary" className="w-full justify-center" loading={setup.isPending}>
          Create account
        </Button>
      </form>
    </AuthCard>
  );
}
