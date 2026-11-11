// Passkeys in the browser: the server's options come as JSON with binary
// fields in base64url; navigator.credentials wants ArrayBuffers, and its
// answer goes back as JSON with base64url again. (PublicKeyCredential's own
// parse…FromJSON / toJSON are not everywhere yet, so this does it by hand.)

/** The answer of every …/begin route. */
export type CeremonyStart = { ceremony: string; publicKey: Record<string, unknown> };

export function passkeysSupported(): boolean {
  return typeof window !== "undefined" && typeof window.PublicKeyCredential === "function" && !!navigator.credentials;
}

export function fromB64url(s: string): ArrayBuffer {
  const b64 = s.replace(/-/g, "+").replace(/_/g, "/");
  const bin = atob(b64 + "=".repeat((4 - (b64.length % 4)) % 4));
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out.buffer;
}

export function toB64url(buf: ArrayBuffer | ArrayBufferView): string {
  const bytes = buf instanceof ArrayBuffer ? new Uint8Array(buf) : new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength);
  let bin = "";
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

type Descriptor = { id: string; type: string; transports?: string[] };

const descriptors = (list: unknown) =>
  Array.isArray(list) ? (list as Descriptor[]).map((d) => ({ ...d, id: fromB64url(d.id) })) : undefined;

export function creationOptions(json: Record<string, unknown>): PublicKeyCredentialCreationOptions {
  const user = json.user as { id: string; name: string; displayName: string };
  return {
    ...(json as unknown as PublicKeyCredentialCreationOptions),
    challenge: fromB64url(json.challenge as string),
    user: { ...user, id: fromB64url(user.id) },
    excludeCredentials: descriptors(json.excludeCredentials) as PublicKeyCredentialDescriptor[] | undefined,
  };
}

export function requestOptions(json: Record<string, unknown>): PublicKeyCredentialRequestOptions {
  return {
    ...(json as unknown as PublicKeyCredentialRequestOptions),
    challenge: fromB64url(json.challenge as string),
    allowCredentials: descriptors(json.allowCredentials) as PublicKeyCredentialDescriptor[] | undefined,
  };
}

/** The credential as the server reads it (the WebAuthn JSON encoding). */
export function credentialJSON(cred: PublicKeyCredential): Record<string, unknown> {
  const r = cred.response as AuthenticatorResponse & Partial<AuthenticatorAttestationResponse & AuthenticatorAssertionResponse>;
  const response: Record<string, unknown> = { clientDataJSON: toB64url(r.clientDataJSON) };
  if (r.attestationObject) {
    response.attestationObject = toB64url(r.attestationObject);
    if (typeof r.getTransports === "function") response.transports = r.getTransports();
  } else {
    if (r.authenticatorData) response.authenticatorData = toB64url(r.authenticatorData);
    if (r.signature) response.signature = toB64url(r.signature);
    if (r.userHandle) response.userHandle = toB64url(r.userHandle);
  }
  return {
    id: cred.id,
    rawId: toB64url(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
    response,
    clientExtensionResults: cred.getClientExtensionResults?.() ?? {},
  };
}

/** The user closed the browser's passkey sheet (or it timed out): not an error to show. */
export function isPasskeyCancel(err: unknown): boolean {
  return err instanceof DOMException && (err.name === "NotAllowedError" || err.name === "AbortError");
}

export async function createPasskey(publicKey: Record<string, unknown>): Promise<Record<string, unknown>> {
  const cred = (await navigator.credentials.create({ publicKey: creationOptions(publicKey) })) as PublicKeyCredential | null;
  if (!cred) throw new DOMException("no passkey", "NotAllowedError");
  return credentialJSON(cred);
}

export async function getPasskey(publicKey: Record<string, unknown>): Promise<Record<string, unknown>> {
  const cred = (await navigator.credentials.get({ publicKey: requestOptions(publicKey) })) as PublicKeyCredential | null;
  if (!cred) throw new DOMException("no passkey", "NotAllowedError");
  return credentialJSON(cred);
}

/** A name for a new passkey from the browser it is made on ("Mac", "iPhone"…). */
export function defaultPasskeyName(ua: string = typeof navigator !== "undefined" ? navigator.userAgent : ""): string {
  if (/iPhone/.test(ua)) return "iPhone";
  if (/iPad/.test(ua)) return "iPad";
  if (/Android/.test(ua)) return "Android";
  if (/Mac OS X|Macintosh/.test(ua)) return "Mac";
  if (/Windows/.test(ua)) return "Windows";
  if (/CrOS/.test(ua)) return "Chromebook";
  if (/Linux/.test(ua)) return "Linux";
  return "Passkey";
}
