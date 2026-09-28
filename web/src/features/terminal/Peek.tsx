// Peek: read a session's screen as text and answer it — without a terminal.
// Refreshes every 3 s; the quick keys cover what Claude usually asks for.

import { CornerDownLeft, Plug, RefreshCw, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { errorMessage } from "@/api/client";
import { useSendKeys, useSessionCapture } from "@/api/hooks";
import { Button } from "@/components/ui/Button";
import { Sheet } from "@/components/ui/Dialog";
import { useToast } from "@/components/ui/Toast";
import { attachPath, PEEK_KEYS } from "@/lib/terminal";

export function PeekPanel({
  runnerId,
  hostName,
  session,
  window = null,
  windowLabel,
  onClose,
  showAttach = true,
}: {
  runnerId: number;
  hostName: string;
  session: string;
  /** A window index to read and type into; null = the session's current window. */
  window?: number | null;
  windowLabel?: string;
  onClose: () => void;
  showAttach?: boolean;
}) {
  const capture = useSessionCapture(runnerId, session, window);
  const send = useSendKeys(runnerId, session, window);
  const toast = useToast();
  const navigate = useNavigate();
  const [text, setText] = useState("");
  const [enter, setEnter] = useState(true);
  const screen = useRef<HTMLPreElement>(null);
  const stick = useRef(true);

  // Follow the bottom of the screen unless the reader scrolled up.
  useEffect(() => {
    const el = screen.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [capture.data]);

  const sendKeys = (body: { text: string; enter: boolean }) =>
    send.mutate(body, { onError: (e) => toast.error(e) });

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-3">
        <span className="min-w-0 flex-1 truncate text-[13px]">
          <span className="text-fg-3">Peek · {hostName} · </span>
          <span className="font-mono font-medium">
            {window !== null ? `${session}:${window}` : session}
          </span>
          {windowLabel ? <span className="text-fg-3"> · {windowLabel}</span> : null}
        </span>
        {capture.isFetching ? <RefreshCw className="size-3.5 animate-spin text-fg-3" aria-label="Refreshing" /> : null}
        {showAttach ? (
          <Button size="sm" variant="subtle" onClick={() => navigate(attachPath(runnerId, session, window))}>
            <Plug className="size-3.5" aria-hidden /> Attach
          </Button>
        ) : null}
        <button type="button" onClick={onClose} className="rounded-md p-1.5 text-fg-3 hover:bg-surface-2 hover:text-fg" aria-label="Close peek">
          <X className="size-4" />
        </button>
      </div>
      <pre
        ref={screen}
        onScroll={(e) => {
          const el = e.currentTarget;
          stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
        }}
        aria-label="Session screen"
        // Focus lands here, not on the reply box, so opening a peek on a phone
        // does not throw up the keyboard over the screen being read.
        tabIndex={0}
        data-autofocus
        className="min-h-0 flex-1 overflow-auto bg-surface-2/60 px-3 py-2 font-mono text-[12px] leading-[1.35] whitespace-pre text-fg"
      >
        {capture.error ? (
          <span className="text-critical-ink">{errorMessage(capture.error)}</span>
        ) : capture.data === undefined ? (
          <span className="text-fg-3">Loading screen…</span>
        ) : (
          capture.data || <span className="text-fg-3">(empty screen)</span>
        )}
      </pre>
      <div className="safe-bottom shrink-0 space-y-2 border-t border-line p-3">
        <div className="flex flex-wrap gap-1.5" role="group" aria-label="Quick keys">
          {PEEK_KEYS.map((k) => (
            <Button key={k.label} size="sm" variant="subtle" title={k.title} disabled={send.isPending} onClick={() => sendKeys({ text: k.text, enter: k.enter })}>
              {k.label}
            </Button>
          ))}
        </div>
        <form
          className="flex items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (!text && !enter) return;
            sendKeys({ text, enter });
            setText("");
          }}
        >
          <input
            aria-label="Keys to send"
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder="Type a reply…"
            autoComplete="off"
            autoCapitalize="off"
            spellCheck={false}
            className="h-8.5 min-w-0 flex-1 rounded-md border border-line-strong bg-surface px-2.5 font-mono text-[13px] focus:border-accent focus:outline-none"
          />
          <label className="flex shrink-0 items-center gap-1.5 text-[12px] text-fg-2">
            <input type="checkbox" checked={enter} onChange={(e) => setEnter(e.target.checked)} className="accent-[var(--accent)]" />
            Enter
          </label>
          <Button type="submit" size="sm" variant="primary" loading={send.isPending} aria-label="Send keys">
            <CornerDownLeft className="size-3.5" aria-hidden /> Send
          </Button>
        </form>
      </div>
    </div>
  );
}

export function PeekSheet(props: {
  runnerId: number;
  hostName: string;
  session: string;
  window?: number | null;
  windowLabel?: string;
  onClose: () => void;
}) {
  return (
    <Sheet open onClose={props.onClose} label={`Peek at ${props.session}`}>
      <PeekPanel {...props} />
    </Sheet>
  );
}
