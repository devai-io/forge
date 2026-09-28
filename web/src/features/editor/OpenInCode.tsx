import { Code } from "lucide-react";
import { Button, type ButtonProps } from "@/components/ui/Button";
import type { EditorTarget } from "@/lib/code";
import { useEditorLauncher } from "./launcher";

/**
 * "Open in VS Code". When the editor is unavailable the button is disabled and
 * the reason is the tooltip — on a wrapper, because disabled buttons don't
 * receive the hover that shows a title.
 */
export function OpenInCodeButton({
  target,
  label = "Open in VS Code",
  iconOnly = false,
  responsiveLabel = false,
  ...props
}: { target: EditorTarget; label?: string; iconOnly?: boolean; responsiveLabel?: boolean } & Omit<ButtonProps, "onClick" | "children">) {
  const code = useEditorLauncher();
  return (
    <span title={code.reason} className="inline-flex">
      <Button
        {...props}
        disabled={!code.available}
        aria-label={iconOnly || responsiveLabel ? label : undefined}
        onClick={() => code.open(target)}
      >
        <Code className="size-3.5" aria-hidden />
        {iconOnly ? null : responsiveLabel ? <span className="hidden sm:inline">{label}</span> : label}
      </Button>
    </span>
  );
}
