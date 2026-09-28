import { FolderKanban, Plus } from "lucide-react";
import { useState } from "react";
import { useProjects } from "@/api/hooks";
import { Button } from "@/components/ui/Button";
import { EmptyState, ErrorState } from "@/components/ui/EmptyState";
import { Switch } from "@/components/ui/Input";
import { PageHeader } from "@/components/ui/Panel";
import { Skeleton } from "@/components/ui/Skeleton";
import { Segmented } from "@/components/ui/Tabs";
import { useUser } from "@/lib/auth";
import { todayInTz } from "@/lib/format";
import { ProjectCard } from "./ProjectCard";
import { ProjectDialog } from "./ProjectDialog";

export function ProjectsPage() {
  const [archived, setArchived] = useState(false);
  const [category, setCategory] = useState<"all" | "work" | "personal">("all");
  const [creating, setCreating] = useState(false);
  const projects = useProjects(archived);
  const user = useUser();
  const today = todayInTz(user.timezone);
  const list = (projects.data ?? []).filter((p) => category === "all" || p.category === category);

  return (
    <div className="mx-auto max-w-[1400px]">
      <PageHeader
        title="Projects"
        subtitle="Everything you are building, running or keeping on the radar, by priority."
        actions={
          <>
            <Segmented
              label="Category"
              value={category}
              onChange={setCategory}
              items={[
                { value: "all", label: "All" },
                { value: "work", label: "Work" },
                { value: "personal", label: "Personal" },
              ]}
            />
            <Switch checked={archived} onChange={setArchived} label="Archived" />
            <Button variant="primary" size="sm" onClick={() => setCreating(true)}>
              <Plus className="size-3.5" aria-hidden /> New project
            </Button>
          </>
        }
      />
      {projects.isPending ? (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 6 }, (_, i) => (
            <Skeleton key={i} className="h-52" />
          ))}
        </div>
      ) : projects.error ? (
        <ErrorState error={projects.error} onRetry={() => projects.refetch()} />
      ) : list.length ? (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {list.map((p) => (
            <ProjectCard key={p.id} project={p} today={today} />
          ))}
        </div>
      ) : (
        <EmptyState
          icon={<FolderKanban />}
          title="No projects here"
          action={
            <Button variant="primary" size="sm" onClick={() => setCreating(true)}>
              New project
            </Button>
          }
        />
      )}
      {creating ? <ProjectDialog onClose={() => setCreating(false)} /> : null}
    </div>
  );
}
