import clsx from "clsx";
import { LoaderCircle } from "lucide-react";

export function Spinner({ className }: { className?: string }) {
  return <LoaderCircle aria-hidden className={clsx("animate-spin text-fg-3", className ?? "size-4")} />;
}

export function FullPageSpinner() {
  return (
    <div className="grid min-h-dvh place-items-center" role="status" aria-label="Loading">
      <Spinner className="size-6" />
    </div>
  );
}
