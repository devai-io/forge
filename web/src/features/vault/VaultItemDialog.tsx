// Create or edit a vault item.
//
// Metadata is edited in place. Secret values and the file are never loaded
// into this form: on edit they are either left alone (not sent), replaced
// wholesale, or removed (`null`) — exactly the API's PATCH semantics — so the
// form never needs to reveal anything to change something.

import { Paperclip, Trash2 } from "lucide-react";
import { useState } from "react";
import { useProjects, useSaveVaultItem } from "@/api/hooks";
import type { VaultInput, VaultItem, VaultKind } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Field, Input, Select, Textarea } from "@/components/ui/Input";
import { KeyValueRows } from "@/components/ui/KeyValueRows";
import { Segmented } from "@/components/ui/Tabs";
import { useToast } from "@/components/ui/Toast";
import {
  fileToBase64,
  formatBytes,
  recordToRows,
  rowsToRecord,
  VAULT_HOSTS,
  VAULT_KINDS,
  VAULT_PLATFORMS,
  type KV,
} from "@/lib/vault";
import { SecretKeyChips } from "./bits";

type SecretMode = "keep" | "replace" | "remove";
type FileMode = "keep" | "replace" | "remove";

export function VaultItemDialog({
  item,
  defaults,
  onClose,
  onSaved,
}: {
  item?: VaultItem;
  defaults?: { project_key?: string | null };
  onClose: () => void;
  onSaved?: (item: VaultItem) => void;
}) {
  const editing = !!item;
  const projects = useProjects();
  const save = useSaveVaultItem();
  const toast = useToast();

  const [name, setName] = useState(item?.name ?? "");
  const [kind, setKind] = useState<VaultKind>(item?.kind ?? "password");
  const [projectKey, setProjectKey] = useState(item?.project_key ?? defaults?.project_key ?? "");
  const [platform, setPlatform] = useState(item?.platform ?? "");
  const [host, setHost] = useState(item?.host ?? "");
  const [identifier, setIdentifier] = useState(item?.identifier ?? "");
  const [location, setLocation] = useState(item?.location ?? "");
  const [expiresAt, setExpiresAt] = useState(item?.expires_at ?? "");
  const [tags, setTags] = useState(item?.tags.join(", ") ?? "");
  const [notes, setNotes] = useState(item?.notes ?? "");
  const [fields, setFields] = useState<KV[]>(recordToRows(item?.fields ?? {}));

  const [secretMode, setSecretMode] = useState<SecretMode>(editing ? "keep" : "replace");
  const [secretRows, setSecretRows] = useState<KV[]>(
    editing && item.secret_keys.length ? item.secret_keys.map((k) => ({ key: k, value: "" })) : [{ key: "value", value: "" }],
  );
  const [fileMode, setFileMode] = useState<FileMode>(editing ? "keep" : "replace");
  const [file, setFile] = useState<File | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const body: VaultInput = {
      name: name.trim(),
      kind,
      project_key: projectKey || null,
      platform: platform.trim(),
      host: host.trim(),
      identifier: identifier.trim(),
      fields: rowsToRecord(fields),
      location: location.trim(),
      expires_at: expiresAt || null,
      notes,
      tags: tags
        .split(",")
        .map((t) => t.trim())
        .filter(Boolean),
    };
    if (secretMode === "replace") {
      const secret = rowsToRecord(secretRows);
      if (Object.keys(secret).length) body.secret = secret;
      else if (editing) body.secret = null;
    } else if (secretMode === "remove") {
      body.secret = null;
    }
    if (fileMode === "replace" && file) {
      try {
        body.file = { name: file.name, content_base64: await fileToBase64(file) };
      } catch (err) {
        toast.error(err);
        return;
      }
    } else if (fileMode === "remove") {
      body.file = null;
    }
    save.mutate(
      { id: item?.id, ...body },
      {
        onSuccess: (saved) => {
          toast.success(editing ? "Vault item saved" : "Added to the vault");
          onSaved?.(saved);
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
      title={editing ? `Edit ${item.name}` : "Add to the vault"}
      description="Encrypted at rest. Secret values are write-only here — use Reveal on the item to read them."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="vault-form" loading={save.isPending} disabled={!name.trim()}>
            {editing ? "Save" : "Add item"}
          </Button>
        </>
      }
    >
      <form id="vault-form" onSubmit={submit} className="space-y-4" autoComplete="off">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_190px]">
          <Field label="Name">
            {(id) => (
              <Input id={id} value={name} onChange={(e) => setName(e.target.value)} required maxLength={200} placeholder="App Store Connect API key" />
            )}
          </Field>
          <Field label="Kind">
            {(id) => (
              <Select id={id} value={kind} onChange={(e) => setKind(e.target.value as VaultKind)}>
                {VAULT_KINDS.map((k) => (
                  <option key={k.value} value={k.value}>
                    {k.label}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        </div>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <Field label="Project">
            {(id) => (
              <Select id={id} value={projectKey} onChange={(e) => setProjectKey(e.target.value)}>
                <option value="">Shared</option>
                {projects.data?.map((p) => (
                  <option key={p.key} value={p.key}>
                    {p.key}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Field label="Platform">
            {(id) => (
              <>
                <Input id={id} list="vault-platforms" value={platform} onChange={(e) => setPlatform(e.target.value)} placeholder="ios" />
                <datalist id="vault-platforms">
                  {VAULT_PLATFORMS.map((p) => (
                    <option key={p} value={p} />
                  ))}
                </datalist>
              </>
            )}
          </Field>
          <Field label="Host">
            {(id) => (
              <>
                <Input id={id} list="vault-hosts" value={host} onChange={(e) => setHost(e.target.value)} placeholder="mac" />
                <datalist id="vault-hosts">
                  {VAULT_HOSTS.map((h) => (
                    <option key={h} value={h} />
                  ))}
                </datalist>
              </>
            )}
          </Field>
          <Field label="Expires">
            {(id) => <Input id={id} type="date" value={expiresAt} onChange={(e) => setExpiresAt(e.target.value)} />}
          </Field>
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="Identifier" hint="Non-secret handle: username, e-mail, key id, alias">
            {(id, desc) => <Input id={id} aria-describedby={desc} value={identifier} onChange={(e) => setIdentifier(e.target.value)} />}
          </Field>
          <Field label="Location" hint="Where the original lives: path, sops key, GitHub secret">
            {(id, desc) => (
              <Input id={id} aria-describedby={desc} value={location} onChange={(e) => setLocation(e.target.value)} className="font-mono text-[13px]" />
            )}
          </Field>
        </div>

        <fieldset className="space-y-2">
          <legend className="mb-1 text-[13px] font-medium text-fg-2">Metadata fields</legend>
          <p className="text-xs text-fg-3">Non-secret details — issuer id, team id, bundle id, fingerprint. Shown in the clear.</p>
          <KeyValueRows label="Metadata field" rows={fields} onChange={setFields} keyPlaceholder="issuer_id" />
        </fieldset>

        <fieldset className="space-y-2 rounded-lg border border-line p-3">
          <legend className="px-1 text-[13px] font-medium text-fg-2">Secret values</legend>
          {editing ? (
            <div className="flex flex-wrap items-center gap-2">
              <Segmented
                size="sm"
                label="Secret values"
                value={secretMode}
                onChange={setSecretMode}
                items={[
                  { value: "keep", label: "Keep" },
                  { value: "replace", label: "Replace" },
                  { value: "remove", label: "Remove" },
                ]}
              />
              {item.secret_keys.length ? <SecretKeyChips keys={item.secret_keys} /> : <span className="text-xs text-fg-3">none stored</span>}
            </div>
          ) : null}
          {secretMode === "replace" ? (
            <>
              {editing ? <p className="text-xs text-fg-3">Replaces every stored secret field with the rows below.</p> : null}
              <KeyValueRows label="Secret field" secret rows={secretRows} onChange={setSecretRows} keyPlaceholder="value" valuePlaceholder="secret" />
            </>
          ) : secretMode === "remove" ? (
            <p className="text-xs text-critical-ink">All stored secret values will be deleted on save.</p>
          ) : null}
        </fieldset>

        <fieldset className="space-y-2 rounded-lg border border-line p-3">
          <legend className="px-1 text-[13px] font-medium text-fg-2">File</legend>
          {editing && item.has_file ? (
            <div className="flex flex-wrap items-center gap-2 text-[13px]">
              <Segmented
                size="sm"
                label="File"
                value={fileMode}
                onChange={setFileMode}
                items={[
                  { value: "keep", label: "Keep" },
                  { value: "replace", label: "Replace" },
                  { value: "remove", label: "Remove" },
                ]}
              />
              <span className="inline-flex items-center gap-1 text-fg-2">
                <Paperclip className="size-3.5" aria-hidden /> {item.file_name} · {formatBytes(item.file_size)}
              </span>
            </div>
          ) : null}
          {fileMode === "replace" || (editing && !item.has_file && fileMode === "keep") ? (
            <div className="flex flex-wrap items-center gap-2">
              <input
                type="file"
                aria-label="File"
                onChange={(e) => {
                  setFile(e.target.files?.[0] ?? null);
                  setFileMode("replace");
                }}
                className="text-[13px] file:mr-3 file:rounded-md file:border file:border-line-strong file:bg-surface-2 file:px-2.5 file:py-1 file:text-[13px] file:text-fg"
              />
              {file ? <span className="text-xs text-fg-3">{formatBytes(file.size)}</span> : null}
              {file ? (
                <Button size="sm" variant="ghost" onClick={() => setFile(null)} aria-label="Clear chosen file">
                  <Trash2 className="size-3.5" />
                </Button>
              ) : null}
            </div>
          ) : fileMode === "remove" ? (
            <p className="text-xs text-critical-ink">The stored file will be deleted on save.</p>
          ) : null}
          <p className="text-xs text-fg-3">Keystores, .p8/.p12 files, provisioning profiles, service-account JSON.</p>
        </fieldset>

        <Field label="Tags" hint={'Comma separated. "integration:grafana" wires a token into monitoring.'}>
          {(id, desc) => <Input id={id} aria-describedby={desc} value={tags} onChange={(e) => setTags(e.target.value)} />}
        </Field>
        <Field label="Notes" hint="Markdown — how to rotate, who issued it, gotchas. Not secret.">
          {(id, desc) => (
            <Textarea id={id} aria-describedby={desc} rows={4} value={notes} onChange={(e) => setNotes(e.target.value)} className="font-mono text-[13px]" />
          )}
        </Field>
      </form>
    </Dialog>
  );
}
