import { useMe, useMemberAgent } from "@/api/hooks";
import { BookOpen, Bot, ClipboardCheck, Code, Cpu, FolderKanban, Gauge, LayoutDashboard, ListTodo, Lock, Server, Sparkles, SquareTerminal } from "lucide-react";

// `mobile`: gets its own slot in the phone tab bar — five at most, beside "More"
// (AppShell's grid-cols-6); the rest live under "More".
export const NAV = [
  { to: "/", label: "Dashboard", short: "Home", icon: LayoutDashboard, end: true, mobile: true },
  { to: "/assistant", label: "Assistant", short: "Chat", icon: Sparkles, end: false, mobile: true, keywords: "chat ai claude deepseek openrouter delegate llm ask" },
  { to: "/terminal", label: "Terminal", short: "Terminal", icon: SquareTerminal, end: false, mobile: true, keywords: "tmux shell claude sessions attach" },
  { to: "/editor", label: "Editor", short: "Editor", icon: Code, end: false, mobile: false, keywords: "vs code vscode ide edit" },
  { to: "/checkup", label: "Check-up", short: "Check-up", icon: ClipboardCheck, end: false, mobile: false, keywords: "daily health checkup actions" },
  { to: "/tasks", label: "Tasks", short: "Tasks", icon: ListTodo, end: false, mobile: true },
  { to: "/projects", label: "Projects", short: "Projects", icon: FolderKanban, end: false, mobile: false },
  { to: "/infra", label: "Infrastructure", short: "Infra", icon: Server, end: false, mobile: false },
  { to: "/monitoring", label: "Monitoring", short: "Monitoring", icon: Gauge, end: false, mobile: false, keywords: "grafana metrics alerts nomad hosts probes" },
  { to: "/vault", label: "Vault", short: "Vault", icon: Lock, end: false, mobile: false, keywords: "secrets keys passwords tokens keystore certificates" },
  { to: "/agents", label: "Agents", short: "Agents", icon: Bot, end: false, mobile: true, keywords: "runs sessions claude running" },
  // A member's own agent (the owner gave it one): its page instead of Agents.
  { to: "/agent", label: "Agent", short: "Agent", icon: Bot, end: false, mobile: true, keywords: "claude ask chat agent" },
  { to: "/machines", label: "Machines", short: "Machines", icon: Cpu, end: false, mobile: false, keywords: "runners agents pair add machine master worker" },
  { to: "/docs", label: "Docs", short: "Docs", icon: BookOpen, end: false, mobile: false, keywords: "documentation help how it works what forge does security" },
] as const;

/** What a limited (member) account may open; the server refuses the rest. */
const MEMBER_PATHS: string[] = ["/tasks", "/projects", "/agents"];
/** With an agent of its own, that agent's page takes the place of Agents. */
const MEMBER_AGENT_PATHS: string[] = ["/tasks", "/projects", "/agent"];

export function isMember(user: { role?: string } | undefined): boolean {
  return user?.role === "member";
}

/** The pages in the menu for this account. */
export function useNav(): readonly (typeof NAV)[number][] {
  const role = useMe().data?.user.role;
  const member = role === "member";
  const agent = useMemberAgent(member).data?.agent;
  if (!member) return NAV.filter((n) => n.to !== "/agent");
  const paths = agent ? MEMBER_AGENT_PATHS : MEMBER_PATHS;
  return NAV.filter((n) => paths.includes(n.to));
}
