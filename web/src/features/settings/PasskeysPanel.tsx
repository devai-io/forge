// Settings → Passkeys: the main way into Forge. Adding one asks for the
// password (or another passkey) first; so does removing one.

import { Check, KeyRound, Pencil, Plus, Trash2, X } from "lucide-react";
import { useState } from "react";
import { ApiError } from "@/api/client";
import { useAddPasskey, useDeletePasskey, usePasskeys, useRenamePasskey } from "@/api/hooks";
import type { Passkey } from "@/api/types";
import { Badge } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/Dialog";
import { Input } from "@/components/ui/Input";
import { Panel } from "@/components/ui/Panel";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { SkeletonRows } from "@/components/ui/Skeleton";
import { useToast } from "@/components/ui/Toast";
import { defaultPasskeyName, isPasskeyCancel, passkeysSupported } from "@/lib/webauthn";

export function PasskeysPanel() {
  const list = usePasskeys();
  const add = useAddPasskey();
  const toast = useToast();
  const passkeys = list.data?.passkeys ?? [];
  const available = list.data?.available ?? true;
  const supported = passkeysSupported();

  const addOne = () =>
    add.mutate(defaultPasskeyName(), {
      onSuccess: (p) => toast.success(`Passkey "${p.name}" added — sign in with it next time`),
      onError: (err) => {
        if (isPasskeyCancel(err)) return;
        if (err instanceof ApiError && err.code === "elevation_required") return; // cancelled "Confirm it's you"
        if (err instanceof DOMException && err.name === "InvalidStateError") {
          toast.error("This device already has a passkey for your account.");
          return;
        }
        toast.error(err);
      },
    });

  return (
    <Panel
      title={
        <span className="flex items-center gap-2">
          Passkeys
          {list.isPending ? null : passkeys.length ? (
            <Badge tone="good" dot>
              {passkeys.length}
            </Badge>
          ) : (
            <Badge tone="warning" dot title="Without one, every sign-in needs the password and the authenticator code">
              none — recommended
            </Badge>
          )}
        </span>
      }
      id="settings-passkeys"
      bodyClassName="p-0"
      actions={
        supported && available ? (
          <Button size="sm" variant="primary" loading={add.isPending} onClick={addOne}>
            <Plus className="size-3.5" aria-hidden /> Add a passkey
          </Button>
        ) : null
      }
    >
      <p className="px-4 pt-3 pb-1 text-[13px] text-fg-2">
        A passkey signs you in with Face ID, Touch ID, Windows Hello or your phone — no password, no code — and
        confirms sensitive actions. Signing in with the password instead always needs the authenticator code too.
      </p>
      {!available ? (
        <p className="px-4 py-2 text-[13px] text-fg-3">
          This server cannot use passkeys: its public URL has to be a domain name (not an IP address).
        </p>
      ) : !supported ? (
        <p className="px-4 py-2 text-[13px] text-fg-3">This browser cannot use passkeys.</p>
      ) : null}
      {list.isPending ? (
        <SkeletonRows rows={1} className="p-3" />
      ) : passkeys.length ? (
        <ul className="mt-2 divide-y divide-line border-t border-line">
          {passkeys.map((p) => (
            <PasskeyRow key={p.id} passkey={p} />
          ))}
        </ul>
      ) : (
        <div className="h-3" />
      )}
    </Panel>
  );
}

function PasskeyRow({ passkey: p }: { passkey: Passkey }) {
  const rename = useRenamePasskey();
  const remove = useDeletePasskey();
  const toast = useToast();
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(p.name);
  const [confirming, setConfirming] = useState(false);

  return (
    <li className="flex flex-wrap items-center gap-3 px-4 py-3">
      <KeyRound className="size-4 text-fg-3" aria-hidden />
      <div className="min-w-0 flex-1">
        {editing ? (
          <form
            className="flex items-center gap-1.5"
            onSubmit={(e) => {
              e.preventDefault();
              rename.mutate(
                { id: p.id, name },
                { onSuccess: () => setEditing(false), onError: (err) => toast.error(err) },
              );
            }}
          >
            <Input
              aria-label="Passkey name"
              value={name}
              maxLength={60}
              autoFocus
              onChange={(e) => setName(e.target.value)}
              className="h-7 max-w-56 text-[13px]"
            />
            <Button size="icon-sm" variant="ghost" type="submit" aria-label="Save name" loading={rename.isPending}>
              <Check className="size-3.5" />
            </Button>
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label="Cancel"
              onClick={() => {
                setName(p.name);
                setEditing(false);
              }}
            >
              <X className="size-3.5" />
            </Button>
          </form>
        ) : (
          <p className="flex items-center gap-2 text-[13px] font-medium">
            {p.name}
            <Badge tone={p.synced ? "accent" : "neutral"} title={p.synced ? "Backed up by its provider, on all your devices" : "Kept on that one device or key"}>
              {p.synced ? "synced" : "this device only"}
            </Badge>
          </p>
        )}
        <p className="truncate text-[11.5px] text-fg-3">
          added <RelativeTime iso={p.created_at} /> ·{" "}
          {p.last_used_at ? (
            <>
              last used <RelativeTime iso={p.last_used_at} />
            </>
          ) : (
            "not used yet"
          )}
        </p>
      </div>
      {!editing ? (
        <div className="flex items-center gap-1">
          <Button size="icon-sm" variant="ghost" aria-label={`Rename ${p.name}`} onClick={() => setEditing(true)}>
            <Pencil className="size-3.5" />
          </Button>
          <Button size="icon-sm" variant="ghost" aria-label={`Remove ${p.name}`} onClick={() => setConfirming(true)}>
            <Trash2 className="size-3.5" />
          </Button>
        </div>
      ) : null}
      <ConfirmDialog
        open={confirming}
        onClose={() => setConfirming(false)}
        title={`Remove the passkey "${p.name}"?`}
        body="It will no longer sign you in. Remove it from the device's password manager too."
        confirmLabel="Remove"
        loading={remove.isPending}
        onConfirm={() =>
          remove.mutate(p.id, {
            onSuccess: () => {
              setConfirming(false);
              toast.success("Passkey removed");
            },
            onError: (err) => {
              setConfirming(false);
              if (!(err instanceof ApiError && err.code === "elevation_required")) toast.error(err);
            },
          })
        }
      />
    </li>
  );
}
