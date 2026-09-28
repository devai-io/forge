import { BookOpen, Bot, ClipboardCheck, Code, FolderKanban, Gauge, LayoutDashboard, ListTodo, Lock, Server, Sparkles, SquareTerminal } from "lucide-react";

// `mobile`: gets its own slot in the phone tab bar — five at most, beside "More"
// (AppShell's grid-cols-6); the rest live under "More".
export const NAV = [
  { to: "/", label: "Dashboard", short: "Home", icon: LayoutDashboard, end: true, mobile: true },
  { to: "/assistant", label: "Assistant", short: "Chat", icon: Sparkles, end: false, mobile: true, keywords: "chat ai deepseek delegate llm ask" },
  { to: "/terminal", label: "Terminal", short: "Terminal", icon: SquareTerminal, end: false, mobile: true, keywords: "tmux shell claude sessions attach" },
  { to: "/editor", label: "Editor", short: "Editor", icon: Code, end: false, mobile: false, keywords: "vs code vscode ide edit" },
  { to: "/checkup", label: "Check-up", short: "Check-up", icon: ClipboardCheck, end: false, mobile: false, keywords: "daily health checkup actions" },
  { to: "/tasks", label: "Tasks", short: "Tasks", icon: ListTodo, end: false, mobile: true },
  { to: "/projects", label: "Projects", short: "Projects", icon: FolderKanban, end: false, mobile: false },
  { to: "/infra", label: "Infrastructure", short: "Infra", icon: Server, end: false, mobile: false },
  { to: "/monitoring", label: "Monitoring", short: "Monitoring", icon: Gauge, end: false, mobile: false, keywords: "grafana metrics alerts nomad hosts probes" },
  { to: "/vault", label: "Vault", short: "Vault", icon: Lock, end: false, mobile: false, keywords: "secrets keys passwords tokens keystore certificates" },
  { to: "/agents", label: "Agents", short: "Agents", icon: Bot, end: false, mobile: true },
  { to: "/docs", label: "Docs", short: "Docs", icon: BookOpen, end: false, mobile: false, keywords: "documentation help how it works what forge does security" },
] as const;
