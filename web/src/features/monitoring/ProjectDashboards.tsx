import { LayoutDashboard } from "lucide-react";
import { useMonitoring } from "@/api/hooks";
import { Panel } from "@/components/ui/Panel";
import { dashboardsForProject } from "@/lib/monitoring";
import { DashboardLink } from "./MonitoringPage";

/**
 * Grafana dashboards that look like they belong to this project (title or
 * folder names it). Best effort and silent: nothing renders when monitoring is
 * unavailable or nothing matches.
 */
export function ProjectDashboards({ project }: { project: { key: string; name: string } }) {
  const mon = useMonitoring();
  const matches = mon.data ? dashboardsForProject(mon.data.grafana.dashboards, project) : [];
  if (!matches.length) return null;
  return (
    <Panel title="Dashboards" icon={<LayoutDashboard />} id="proj-dashboards">
      {matches.map((d) => (
        <DashboardLink key={d.uid || d.url} title={d.folder ? `${d.title} · ${d.folder}` : d.title} url={d.url} />
      ))}
      <p className="px-2 pt-1 text-[11px] text-fg-3">Grafana — over Tailscale.</p>
    </Panel>
  );
}
