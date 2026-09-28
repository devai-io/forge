import { useNavigate } from "react-router-dom";
import { useCodeStatus } from "@/api/hooks";
import { editorPath, type EditorTarget } from "@/lib/code";

/**
 * Whether VS Code can open right now, why not, and a way to open a target.
 * Opening navigates to /editor, which reuses this tab's open for the target or
 * POSTs a new one (behind the confirm-it's-you prompt).
 */
export function useEditorLauncher() {
  const status = useCodeStatus();
  const navigate = useNavigate();
  const available = status.data?.available ?? false;
  const runnerName = status.data?.runner_name ?? null;
  const reason = status.data
    ? status.data.available
      ? `Open in VS Code on ${runnerName ?? "the master"}`
      : status.data.reason || "VS Code is not available"
    : status.error
      ? "Couldn't check whether VS Code is available"
      : "Checking VS Code…";
  return { available, runnerName, reason, open: (target: EditorTarget) => navigate(editorPath(target)) };
}
