import { lazy, Suspense } from "react";
import { Route, Routes } from "react-router-dom";
import { EmptyState } from "@/components/ui/EmptyState";
import { FullPageSpinner } from "@/components/ui/Spinner";
import { AgentsPage } from "@/features/agents/AgentsPage";
import { RunPage } from "@/features/agents/RunPage";
import { ForgotPage, LoginPage, ResetPage } from "@/features/auth/AuthPages";
import { ElevationGate } from "@/features/auth/ElevationGate";
import { CheckupPage } from "@/features/checkup/CheckupPage";
import { DashboardPage } from "@/features/dashboard/DashboardPage";
import { InfraPage } from "@/features/infra/InfraPage";
import { ProjectPage } from "@/features/projects/ProjectPage";
import { ProjectsPage } from "@/features/projects/ProjectsPage";
import { AppShell } from "@/features/shell/AppShell";
import { TasksPage } from "@/features/tasks/TasksPage";
import { TerminalPage } from "@/features/terminal/TerminalPage";
import { RequireAuth, useAuthListener } from "@/lib/auth";

// Split off what isn't needed on a typical visit: xterm (the terminal view),
// and pages opened now and then — the dashboard, board and lists stay in the
// main bundle.
const TerminalView = lazy(() => import("@/features/terminal/TerminalView").then((m) => ({ default: m.TerminalView })));
const EditorPage = lazy(() => import("@/features/editor/EditorPage").then((m) => ({ default: m.EditorPage })));
const MonitoringPage = lazy(() => import("@/features/monitoring/MonitoringPage").then((m) => ({ default: m.MonitoringPage })));
const SettingsPage = lazy(() => import("@/features/settings/SettingsPage").then((m) => ({ default: m.SettingsPage })));
const VaultPage = lazy(() => import("@/features/vault/VaultPage").then((m) => ({ default: m.VaultPage })));
const DocsPage = lazy(() => import("@/features/docs/DocsPage").then((m) => ({ default: m.DocsPage })));

const page = (el: React.ReactNode) => <Suspense fallback={<FullPageSpinner />}>{el}</Suspense>;

export function App() {
  useAuthListener();
  return (
    <>
      {/* Answers every 403 elevation_required with "Confirm it's you", app-wide. */}
      <ElevationGate />
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/forgot" element={<ForgotPage />} />
        <Route path="/reset" element={<ResetPage />} />
        <Route
          element={
            <RequireAuth>
              <AppShell />
            </RequireAuth>
          }
        >
          <Route index element={<DashboardPage />} />
          <Route path="terminal" element={<TerminalPage />} />
          <Route path="terminal/:runnerId/:session" element={page(<TerminalView />)} />
          <Route path="editor" element={page(<EditorPage />)} />
          <Route path="checkup" element={<CheckupPage />} />
          <Route path="tasks" element={<TasksPage />} />
          <Route path="projects" element={<ProjectsPage />} />
          <Route path="p/:key" element={<ProjectPage />} />
          <Route path="infra" element={<InfraPage />} />
          <Route path="monitoring" element={page(<MonitoringPage />)} />
          <Route path="vault" element={page(<VaultPage />)} />
          <Route path="agents" element={<AgentsPage />} />
          <Route path="agents/runs/:id" element={<RunPage />} />
          <Route path="settings" element={page(<SettingsPage />)} />
          <Route path="docs" element={page(<DocsPage />)} />
          <Route path="*" element={<EmptyState title="Page not found" className="mx-auto mt-10 max-w-md" />} />
        </Route>
      </Routes>
    </>
  );
}
