// Settings → Telegram: Forge in a chat with you. The server long-polls the
// bot (nothing to expose) and only ever talks to the one chat set here.

import { useEffect, useState } from "react";
import { useSetTelegramToken, useTelegram, useTestTelegram, useUpdateTelegram } from "@/api/hooks";
import type { TelegramSettings } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { Field, Input, Switch } from "@/components/ui/Input";
import { Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";

const USES: { key: keyof Omit<TelegramSettings, "enabled" | "chat_id">; label: string; hint: string }[] = [
  {
    key: "prompts",
    label: "Prompts from interactive runs",
    hint: "Questions, tool permissions and plans arrive with buttons (allow, deny, approve, pick an option); reply to one to answer in words or deny with a reason. Each answer is in the security log.",
  },
  {
    key: "runs",
    label: "Runs that finish or wait for you",
    hint: "A finished run says how it went; an interactive one that ends its turn quotes Claude — reply to continue the session, or end it.",
  },
  { key: "checkup", label: "The daily check-up", hint: "The scheduled check-up's actions, as it runs." },
  {
    key: "assistant",
    label: "Talk to the Assistant",
    hint: "Anything else you write goes to the Assistant (it needs its own setup); /new starts a fresh chat. /tasks, /runs, /checkup, /remember and /recall work either way.",
  },
];

export function TelegramPanel() {
  const tg = useTelegram();
  const update = useUpdateTelegram();
  const setToken = useSetTelegramToken();
  const test = useTestTelegram();
  const toast = useToast();
  const [token, setTokenText] = useState("");
  const [chat, setChat] = useState("");

  const current = tg.data?.settings.chat_id ?? 0;
  useEffect(() => setChat(current ? String(current) : ""), [current]);

  const save = (patch: Partial<TelegramSettings>) => update.mutate(patch, { onError: (e) => toast.error(e) });
  const chatValid = /^-?\d{1,20}$/.test(chat.trim()) && chat.trim() !== "0";

  return (
    <Panel title="Telegram" id="settings-telegram" bodyClassName="space-y-4 p-4">
      {tg.isPending ? (
        <SkeletonRows rows={3} />
      ) : tg.error ? (
        <p className="text-[13px] text-critical-ink">Could not load: {String(tg.error)}</p>
      ) : (
        <>
          <p className="text-[13px] text-fg-2">
            Answer Claude's prompts, follow runs and talk to the Assistant from your phone. Create a bot with @BotFather, save
            its token here, send the bot any message — it replies with your chat id — and enter that id.
          </p>
          <div className="flex flex-wrap items-center gap-3">
            <Switch
              checked={tg.data.settings.enabled}
              onChange={(v) => save({ enabled: v })}
              label="Use Telegram"
              disabled={!tg.data.token_configured && !tg.data.settings.enabled}
            />
            {tg.data.token_configured ? <Badge tone="good">token in the vault</Badge> : <Badge tone="warning">no token yet</Badge>}
            {tg.data.settings.chat_id ? <Badge tone="good">chat linked</Badge> : <Badge tone="warning">no chat yet</Badge>}
            {tg.data.bot ? <span className="text-[13px] text-fg-2">@{tg.data.bot}</span> : null}
            {tg.data.token_configured ? (
              <Button
                size="sm"
                variant="subtle"
                loading={test.isPending}
                onClick={() =>
                  test.mutate(undefined, {
                    onSuccess: (r) =>
                      r.ok
                        ? toast.success(r.sent ? "Sent a test message" : (r.note ?? "The token works"))
                        : toast.error(r.error ?? "Telegram did not answer"),
                    onError: (e) => toast.error(e),
                  })
                }
              >
                Test
              </Button>
            ) : null}
          </div>

          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              update.mutate(
                { chat_id: Number(chat.trim()) },
                { onSuccess: () => toast.success("Chat linked"), onError: (err) => toast.error(err) },
              );
            }}
          >
            <Field
              label="Chat id"
              hint="The only chat the bot reads and writes. Changing it asks for your password."
              className="min-w-48 flex-1"
            >
              {(id, desc) => (
                <Input
                  id={id}
                  aria-describedby={desc}
                  inputMode="numeric"
                  value={chat}
                  onChange={(e) => setChat(e.target.value)}
                  placeholder="e.g. 123456789"
                />
              )}
            </Field>
            <Button
              type="submit"
              className="mb-6"
              loading={update.isPending}
              disabled={!chatValid || Number(chat.trim()) === current}
            >
              Link chat
            </Button>
          </form>

          <ul className="space-y-3">
            {USES.map((u) => (
              <li key={u.key}>
                <Switch
                  checked={tg.data.settings[u.key]}
                  onChange={(v) => save({ [u.key]: v })}
                  label={u.label}
                  disabled={!tg.data.settings.enabled}
                />
                <p className="mt-0.5 pl-11 text-[12px] text-fg-3">{u.hint}</p>
              </li>
            ))}
          </ul>

          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              setToken.mutate(token.trim(), {
                onSuccess: () => {
                  setTokenText("");
                  toast.success("Token stored in the vault");
                },
                onError: (err) => toast.error(err),
              });
            }}
          >
            <Field
              label={tg.data.token_configured ? "Replace the bot token" : "Bot token"}
              hint="From @BotFather. Sealed in the vault (tag integration:telegram); never shown again."
              className="min-w-64 flex-1"
            >
              {(id, desc) => (
                <Input
                  id={id}
                  aria-describedby={desc}
                  type="password"
                  autoComplete="off"
                  spellCheck={false}
                  value={token}
                  onChange={(e) => setTokenText(e.target.value)}
                  placeholder="123456789:AA…"
                />
              )}
            </Field>
            <Button type="submit" variant="primary" className="mb-6" loading={setToken.isPending} disabled={!token.includes(":")}>
              Save token
            </Button>
          </form>

          {tg.data.last_error ? (
            <p className="text-[12px] text-critical-ink">
              Last error{tg.data.last_error_at ? <> (<RelativeTime iso={tg.data.last_error_at} />)</> : null}: {tg.data.last_error}
            </p>
          ) : tg.data.last_poll_at ? (
            <p className="text-[12px] text-fg-3">
              Listening — last poll <RelativeTime iso={tg.data.last_poll_at} />.
            </p>
          ) : null}
        </>
      )}
    </Panel>
  );
}
