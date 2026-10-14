// Settings → Endpoint checks: how many quick rechecks a failing endpoint gets
// (it shows as a warning meanwhile) before it is called down, and how long
// each recheck waits.

import { useState } from "react";
import { useEndpointSettings, useUpdateEndpointSettings } from "@/api/hooks";
import { Button } from "@/components/ui/Button";
import { Field, Input } from "@/components/ui/Input";
import { Panel } from "@/components/ui/Panel";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";
import { parseDelays } from "@/lib/infra";

export function EndpointsPanel() {
  const settings = useEndpointSettings();
  const update = useUpdateEndpointSettings();
  const toast = useToast();
  const [retries, setRetries] = useState<string | null>(null);
  const [delays, setDelays] = useState<string | null>(null);

  const current = settings.data;
  const retriesValue = Number(retries ?? current?.retries);
  const retriesValid = Number.isInteger(retriesValue) && retriesValue >= 0 && retriesValue <= 5;
  const parsed = parseDelays(delays ?? current?.recheck_seconds.join(", ") ?? "");
  const dirty = retries !== null || delays !== null;

  return (
    <Panel title="Endpoint checks" id="settings-endpoints" bodyClassName="space-y-4 p-4">
      {settings.isPending ? (
        <SkeletonRows rows={2} />
      ) : settings.error || !current ? (
        <p className="text-[13px] text-critical-ink">Could not load: {String(settings.error)}</p>
      ) : (
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (!retriesValid || !parsed) return;
            update.mutate(
              { retries: retriesValue, recheck_seconds: parsed },
              {
                onSuccess: () => {
                  setRetries(null);
                  setDelays(null);
                  toast.success("Endpoint checks saved");
                },
                onError: (err) => toast.error(err),
              },
            );
          }}
        >
          <p className="text-[13px] text-fg-2">
            A failed check marks the endpoint as a warning and rechecks it soon. Only when every recheck fails too is it
            down: then it shows red, lands in the activity feed and fails the check-up. With 0 rechecks the first failure
            is down.
          </p>
          <div className="flex flex-wrap items-end gap-2">
            <Field label="Rechecks before down" error={!retriesValid ? "A whole number from 0 to 5" : undefined} className="w-48">
              {(id) => (
                <Input
                  id={id}
                  type="number"
                  min={0}
                  max={5}
                  step={1}
                  value={retries ?? String(current.retries)}
                  onChange={(e) => setRetries(e.target.value)}
                />
              )}
            </Field>
            <Field
              label="Wait before each recheck (seconds)"
              hint="Comma separated, 10–900 each; the last one repeats."
              error={!parsed ? "1 to 5 numbers from 10 to 900" : undefined}
              className="min-w-56 flex-1"
            >
              {(id, desc) => (
                <Input
                  id={id}
                  aria-describedby={desc}
                  inputMode="numeric"
                  value={delays ?? current.recheck_seconds.join(", ")}
                  onChange={(e) => setDelays(e.target.value)}
                />
              )}
            </Field>
            <Button type="submit" variant="subtle" className="mb-6" loading={update.isPending} disabled={!dirty || !retriesValid || !parsed}>
              Save
            </Button>
          </div>
        </form>
      )}
    </Panel>
  );
}
