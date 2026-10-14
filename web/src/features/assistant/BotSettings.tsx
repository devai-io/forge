// A project bot's settings: on/off, proactive (Forge wakes it with the
// project's events and a daily review), the review's time and the user's
// standing orders — plus "Run a review now", which works either way.

import { ClipboardCheck, Settings2 } from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useBots, useReviewBot, useUpdateBot } from "@/api/hooks";
import type { BotSettings as Settings } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Field, Input, Switch, Textarea } from "@/components/ui/Input";
import { useToast } from "@/components/ui/Toast";

/** The gear in a bot's chat header; opens the settings. */
export function BotSettingsButton({ projectKey }: { projectKey: string }) {
  const bots = useBots();
  const [open, setOpen] = useState(false);
  const bot = bots.data?.find((b) => b.project.key === projectKey);
  if (!bot) return null;
  const label = `${projectKey} bot settings`;
  return (
    <>
      <Button size="icon-sm" variant="ghost" aria-label={label} title={label} onClick={() => setOpen(true)}>
        <Settings2 className="size-3.5" />
      </Button>
      {open ? <BotSettingsDialog projectKey={projectKey} settings={bot.settings} onClose={() => setOpen(false)} /> : null}
    </>
  );
}

export function BotSettingsDialog({ projectKey, settings, onClose }: { projectKey: string; settings: Settings; onClose: () => void }) {
  const update = useUpdateBot(projectKey);
  const review = useReviewBot(projectKey);
  const toast = useToast();
  const navigate = useNavigate();
  const [enabled, setEnabled] = useState(settings.enabled);
  const [proactive, setProactive] = useState(settings.proactive);
  const [reviewAt, setReviewAt] = useState(settings.review_at);
  const [instructions, setInstructions] = useState(settings.instructions);
  const today = settings.proactive_date === new Date().toISOString().slice(0, 10) ? settings.proactive_today : 0;

  const save = (e: React.FormEvent) => {
    e.preventDefault();
    update.mutate(
      { enabled, proactive, review_at: reviewAt, instructions },
      {
        onSuccess: () => {
          toast.success(`${projectKey} bot saved`);
          onClose();
        },
        onError: (err) => toast.error(err),
      },
    );
  };

  return (
    <Dialog
      open
      onClose={onClose}
      size="lg"
      title={`${projectKey} bot`}
      description="Runs on Claude Code on your master machine, in the project's folder."
      footer={
        <>
          <Button
            variant="ghost"
            className="mr-auto"
            loading={review.isPending}
            disabled={!settings.enabled}
            title={settings.enabled ? "A read-only review of the project, now" : "Turn the bot on first"}
            onClick={() =>
              review.mutate(undefined, {
                onSuccess: (thread) => {
                  onClose();
                  navigate(`/assistant/${thread.chat.id}`);
                },
                onError: (err) => toast.error(err),
              })
            }
          >
            <ClipboardCheck className="size-3.5" aria-hidden /> Run a review now
          </Button>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="bot-settings-form" loading={update.isPending}>
            Save
          </Button>
        </>
      }
    >
      <form id="bot-settings-form" onSubmit={save} className="space-y-4">
        <Switch checked={enabled} onChange={setEnabled} label="Enabled" />
        <div className="space-y-1">
          <Switch checked={enabled && proactive} onChange={setProactive} disabled={!enabled} label="Proactive" />
          <p className="text-[12px] text-fg-3">
            Forge wakes the bot itself: when an endpoint goes down or comes back, CI fails, the check-up finds something or a run finishes, and for
            a daily review. Those turns are read-only for code — it updates tasks and memory and proposes the rest. At most {settings.daily_cap} a
            day{today ? ` (${today} today)` : ""}; answers go to Telegram, unless there is nothing to do.
          </p>
        </div>
        <Field label="Daily review" hint="Your local time; empty for no daily review.">
          {(id, desc) => (
            <Input
              id={id}
              aria-describedby={desc}
              type="time"
              value={reviewAt}
              onChange={(e) => setReviewAt(e.target.value)}
              disabled={!enabled || !proactive}
              className="w-36"
            />
          )}
        </Field>
        <Field label="Standing orders" hint="What this bot should always do or keep in mind. At most 4,000 characters.">
          {(id, desc) => (
            <Textarea
              id={id}
              aria-describedby={desc}
              rows={5}
              maxLength={4000}
              value={instructions}
              onChange={(e) => setInstructions(e.target.value)}
              placeholder="Releases go out on Mondays. Keep the board's priorities honest. Never touch the payments service without asking."
            />
          )}
        </Field>
      </form>
    </Dialog>
  );
}
