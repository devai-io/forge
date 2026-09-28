// Accent colour: preset swatches plus a custom colour. Every choice previews
// at once (applyAccent) and is saved to the account; a failed save puts the
// saved accent back. A custom colour is saved once the picker settles, so
// dragging through the spectrum is not a request per pixel.

import clsx from "clsx";
import { Check } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useUpdateMe } from "@/api/hooks";
import { Input } from "@/components/ui/Input";
import { useToast } from "@/components/ui/Toast";
import {
  ACCENTS,
  DEFAULT_ACCENT,
  accentName,
  accentTokens,
  applyAccent,
  normalizeHex,
  resolveAccent,
} from "@/lib/accent";
import { useUser } from "@/lib/auth";
import { useTheme } from "@/lib/theme";

const swatch =
  "relative grid size-8 place-items-center rounded-full border border-line-strong transition-transform hover:scale-110 " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-fg";

export function AccentPicker() {
  const user = useUser();
  const update = useUpdateMe();
  const toast = useToast();
  const { resolved } = useTheme();
  const [preview, setPreview] = useState(user.accent ?? "");
  const shown = resolveAccent(preview);
  const [customOpen, setCustomOpen] = useState(shown.kind === "custom");
  const [hexText, setHexText] = useState(() => accentTokens(preview, "light").accent);
  const colorInput = useRef<HTMLInputElement>(null);

  // The latest values for the debounced save and its flush on unmount.
  const saved = useRef(user.accent ?? "");
  saved.current = user.accent ?? "";
  const pending = useRef<string | null>(null);
  const timer = useRef<number | undefined>(undefined);
  const mutate = useRef(update.mutate);
  mutate.current = update.mutate;

  const save = (value: string) => {
    pending.current = null;
    if (value === saved.current) return;
    mutate.current(
      { accent: value },
      {
        onError: (err) => {
          applyAccent(saved.current);
          setPreview(saved.current);
          toast.error(err);
        },
      },
    );
  };

  useEffect(
    () => () => {
      window.clearTimeout(timer.current);
      if (pending.current !== null) save(pending.current);
    },
    // Runs on unmount only; `save` reads everything it needs through refs.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  const choose = (value: string) => {
    window.clearTimeout(timer.current);
    setPreview(value);
    applyAccent(value);
    save(value);
  };

  const chooseCustom = (raw: string) => {
    const hex = normalizeHex(raw);
    if (!hex) return;
    setPreview(hex);
    applyAccent(hex);
    pending.current = hex;
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => save(hex), 600);
  };

  const pickerValue = normalizeHex(hexText) ?? accentTokens(preview, "light").accent;

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <p className="text-[13px] text-fg-2">Accent colour — saved to your account, so every browser gets it.</p>
        <span className="text-[12px] text-fg-3" aria-live="polite">
          {accentName(preview)}
        </span>
      </div>
      <div role="group" aria-label="Accent colour" className="flex flex-wrap gap-2.5">
        {ACCENTS.map((a) => {
          const pressed = a.id === DEFAULT_ACCENT.id ? shown.kind === "default" : shown.kind === "preset" && shown.preset.id === a.id;
          const t = a[resolved];
          return (
            <button
              key={a.id}
              type="button"
              aria-pressed={pressed}
              aria-label={a.id === DEFAULT_ACCENT.id ? `${a.name} (default)` : a.name}
              title={a.id === DEFAULT_ACCENT.id ? `${a.name} (default)` : a.name}
              onClick={() => {
                setCustomOpen(false);
                choose(a.id === DEFAULT_ACCENT.id ? "" : a.id);
              }}
              className={clsx(swatch, pressed && "ring-2 ring-fg ring-offset-2 ring-offset-surface")}
              style={{ background: t.accent, color: t.fg }}
            >
              {pressed ? <Check className="size-4" aria-hidden /> : null}
            </button>
          );
        })}
        <button
          type="button"
          aria-pressed={shown.kind === "custom"}
          aria-expanded={customOpen}
          aria-label="Custom colour"
          title="Custom colour"
          onClick={() => {
            setCustomOpen(true);
            // Opening straight into the system picker is what a click on "custom" means.
            window.setTimeout(() => colorInput.current?.click(), 0);
          }}
          className={clsx(swatch, "swatch-custom", shown.kind === "custom" && "ring-2 ring-fg ring-offset-2 ring-offset-surface")}
        >
          {shown.kind === "custom" ? (
            <span className="grid size-5 place-items-center rounded-full" style={{ background: accentTokens(preview, resolved).accent }}>
              <Check className="size-3.5" style={{ color: accentTokens(preview, resolved).fg }} aria-hidden />
            </span>
          ) : null}
        </button>
      </div>
      {customOpen ? (
        <div className="flex flex-wrap items-center gap-2.5">
          <label className="inline-flex items-center gap-2 text-[13px] text-fg-2">
            <input
              ref={colorInput}
              type="color"
              value={pickerValue}
              onChange={(e) => {
                setHexText(e.target.value);
                chooseCustom(e.target.value);
              }}
              className="h-8.5 w-12 cursor-pointer rounded-md border border-line-strong bg-surface p-0.5"
            />
            Custom
          </label>
          <Input
            aria-label="Custom colour as hex"
            value={hexText}
            onChange={(e) => {
              setHexText(e.target.value);
              chooseCustom(e.target.value);
            }}
            spellCheck={false}
            autoCapitalize="none"
            maxLength={7}
            placeholder="#2a78d6"
            className="w-28 font-mono"
            aria-invalid={hexText.length > 0 && !normalizeHex(hexText)}
          />
          <p className="basis-full text-[12px] text-fg-3 sm:basis-auto">
            Text on it is picked for contrast; each theme may shift it a little lighter or darker to stay readable.
          </p>
        </div>
      ) : null}
    </div>
  );
}
