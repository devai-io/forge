// The authenticator-app enrolment pieces shared by Settings → Two-factor and
// the sign-in of an account that has none yet: the QR code and the setup key.

import { Copy } from "lucide-react";
import QRCode from "qrcode";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/Button";
import { useToast } from "@/components/ui/Toast";
import { groupSecret } from "@/lib/vault";

/** The otpauth URL as a QR code (an SVG data URL), or null while it renders. */
function useTotpQr(otpauthUrl: string | null): string | null {
  const [qr, setQr] = useState<string | null>(null);
  useEffect(() => {
    setQr(null);
    if (!otpauthUrl) return;
    let cancelled = false;
    // SVG rather than a canvas PNG: pure JS, crisp at any size, and allowed by
    // the CSP as a data: image.
    QRCode.toString(otpauthUrl, { type: "svg", margin: 1, errorCorrectionLevel: "M", color: { dark: "#000000", light: "#ffffff" } }).then(
      (svg) => !cancelled && setQr(`data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`),
      () => !cancelled && setQr(null),
    );
    return () => {
      cancelled = true;
    };
  }, [otpauthUrl]);
  return qr;
}

export function TotpQrCode({ otpauthUrl, size = 220, centered }: { otpauthUrl: string; size?: number; centered?: boolean }) {
  const qr = useTotpQr(otpauthUrl);
  return (
    <div className={centered ? "justify-self-center rounded-lg bg-white p-2" : "justify-self-center rounded-lg bg-white p-2 sm:justify-self-start"}>
      {qr ? (
        <img src={qr} alt="QR code for your authenticator app" width={size} height={size} className="block" />
      ) : (
        <div style={{ width: size, height: size }} />
      )}
    </div>
  );
}

export function SetupKey({ secret }: { secret: string }) {
  const toast = useToast();
  return (
    <div>
      <div className="mb-1 text-fg-3">Setup key</div>
      <div className="flex items-center gap-2">
        <code className="rounded-md border border-line bg-surface-2 px-2 py-1 font-mono text-[13px] break-all">
          {groupSecret(secret)}
        </code>
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label="Copy setup key"
          onClick={() => void navigator.clipboard?.writeText(secret).then(() => toast.success("Key copied"))}
        >
          <Copy className="size-3.5" />
        </Button>
      </div>
    </div>
  );
}
