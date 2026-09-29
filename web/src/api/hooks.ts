// React Query hooks, one per route in the contract.
//
// Keys are arrays whose first element names the resource; invalidation works
// on that prefix. A task mutation touches five views (lists, the drawer, the
// dashboard, the project page's counts and the projects sidebar), so it goes
// through `invalidateTaskViews` rather than each call site remembering them.

import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
} from "@tanstack/react-query";
import { api, ApiError, isElevationError } from "./client";
import { lastSeq, mergeMessages, upsertChat } from "@/lib/chats";
import type {
  Activity,
  AssistantSettings,
  AssistantStatus,
  Chat,
  ChatThread,
  Checkup,
  CheckupSummary,
  MeResponse,
  Monitoring,
  Pairing,
  ProjectFile,
  SetupInput,
  SetupStatus,
  CodeOpenInput,
  SecurityEvent,
  SystemFacts,
  CodeOpenResult,
  CodeStatus,
  RunnerRole,
  NewSessionInput,
  TerminalHosts,
  VaultAudit,
  VaultInput,
  VaultItem,
  Comment,
  Dashboard,
  Endpoint,
  EndpointCheck,
  EndpointInput,
  ProjectDetail,
  Project,
  ProjectInput,
  ProjectServer,
  Repo,
  RepoInput,
  Run,
  RunEvent,
  RunFilters,
  RunInput,
  Runner,
  Server,
  ServerInput,
  Session,
  Task,
  TaskDetail,
  TaskFilters,
  TaskInput,
  User,
  EngineSettings,
  EngineStatus,
  JevSettings,
  JevStatus,
} from "./types";

export const keys = {
  me: ["me"] as const,
  setup: ["setup"] as const,
  sessions: ["sessions"] as const,
  dashboard: ["dashboard"] as const,
  projects: (includeArchived = false) => ["projects", { includeArchived }] as const,
  project: (key: string) => ["project", key] as const,
  projectActivity: (key: string) => ["project-activity", key] as const,
  projectFiles: (key: string) => ["project-files", key] as const,
  tasks: (filters: TaskFilters) => ["tasks", filters] as const,
  task: (id: number) => ["task", id] as const,
  servers: ["servers"] as const,
  endpoints: ["endpoints"] as const,
  endpointChecks: (id: number, hours: number) => ["endpoint-checks", id, hours] as const,
  runners: ["runners"] as const,
  runs: (filters: RunFilters) => ["runs", filters] as const,
  run: (id: number) => ["run", id] as const,
  runEvents: (id: number) => ["run-events", id] as const,
  vault: (filters: VaultFilters) => ["vault", filters] as const,
  vaultItem: (id: number) => ["vault-item", id] as const,
  vaultAudit: ["vault-audit"] as const,
  monitoring: ["monitoring"] as const,
  checkups: ["checkups"] as const,
  checkupLatest: ["checkup", "latest"] as const,
  checkup: (id: number) => ["checkup", id] as const,
  terminalHosts: ["terminal-hosts"] as const,
  codeStatus: ["code-status"] as const,
  security: (kind: string, limit: number) => ["security", kind, limit] as const,
  system: ["system"] as const,
  terminalCapture: (runnerId: number, name: string) => ["terminal-capture", runnerId, name] as const,
  assistant: ["assistant"] as const,
  chats: ["chats"] as const,
  chat: (id: number) => ["chat", id] as const,
};

export type VaultFilters = { project?: string; q?: string; kind?: string };

export function invalidateTaskViews(qc: QueryClient, taskId?: number) {
  void qc.invalidateQueries({ queryKey: ["tasks"] });
  void qc.invalidateQueries({ queryKey: ["dashboard"] });
  void qc.invalidateQueries({ queryKey: ["project"] });
  void qc.invalidateQueries({ queryKey: ["projects"] });
  void qc.invalidateQueries({ queryKey: ["project-activity"] });
  if (taskId !== undefined) void qc.invalidateQueries({ queryKey: keys.task(taskId) });
}

function invalidateProjectViews(qc: QueryClient) {
  void qc.invalidateQueries({ queryKey: ["project"] });
  void qc.invalidateQueries({ queryKey: ["projects"] });
  void qc.invalidateQueries({ queryKey: ["dashboard"] });
}

// ── Auth & account ─────────────────────────────────────────────────────────

/** `null` = signed out. The cache holds the whole MeResponse so `elevated_until` travels with the user. */
export function useMe() {
  return useQuery({
    queryKey: keys.me,
    queryFn: () => api.get<MeResponse>("/auth/me").then((r): MeResponse | null => r),
    retry: false,
    staleTime: 60_000,
  });
}

export function useLogin() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { username: string; password: string; code?: string }) =>
      api.post<MeResponse>("/auth/login", body),
    onSuccess: (me) => {
      qc.clear();
      qc.setQueryData(keys.me, me);
    },
  });
}

/** Whether the server still needs its first account. An API without the route (404) never does. */
export function useSetupStatus() {
  return useQuery({
    queryKey: keys.setup,
    queryFn: () =>
      api.get<SetupStatus>("/setup").catch((err) => {
        if (err instanceof ApiError && err.status === 404) return { needed: false };
        throw err;
      }),
    retry: false,
    staleTime: 60_000,
  });
}

/** Creates the account and signs it in: the answer is a MeResponse, like login's. */
export function useSetup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: SetupInput) => api.post<MeResponse>("/setup", body),
    onSuccess: (me) => {
      qc.clear();
      qc.setQueryData(keys.me, me);
      qc.setQueryData<SetupStatus>(keys.setup, { needed: false });
    },
  });
}

function setMeUser(qc: QueryClient, user: User) {
  qc.setQueryData<MeResponse | null>(keys.me, (old) => ({ user, elevated_until: old?.elevated_until ?? null }));
}

export function useElevate() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { password: string; code?: string }) =>
      api.post<{ elevated_until: string }>("/auth/elevate", body),
    onSuccess: ({ elevated_until }) =>
      qc.setQueryData<MeResponse | null>(keys.me, (old) => (old ? { ...old, elevated_until } : old)),
  });
}

export function useTotpEnable() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (code: string) => api.post<void>("/auth/totp/enable", { code }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.me }),
  });
}

export function useTotpDisable() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { password: string; code: string }) => api.post<void>("/auth/totp/disable", body),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.me }),
  });
}

export function useLogout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<void>("/auth/logout"),
    onSettled: () => {
      qc.clear();
      qc.setQueryData(keys.me, null);
    },
  });
}

export function useUpdateMe() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (
      body: Partial<
        Pick<User, "email" | "display_name" | "timezone" | "weekly_goal" | "checkup_time" | "checkup_email" | "accent">
      >,
    ) => api.patch<{ user: User }>("/auth/me", body).then((r) => r.user),
    onSuccess: (user) => {
      setMeUser(qc, user);
      void qc.invalidateQueries({ queryKey: keys.dashboard });
    },
  });
}

export function useChangePassword() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { current_password: string; new_password: string }) =>
      api.post<void>("/auth/password", body),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.sessions }),
  });
}

export function useForgotPassword() {
  return useMutation({ mutationFn: (login: string) => api.post<void>("/auth/forgot", { login }) });
}

export function useResetPassword() {
  return useMutation({
    mutationFn: (body: { token: string; new_password: string }) => api.post<void>("/auth/reset", body),
  });
}

export function useSessions() {
  return useQuery({
    queryKey: keys.sessions,
    queryFn: () => api.get<{ sessions: Session[] }>("/auth/sessions").then((r) => r.sessions),
  });
}

export function useRevokeSession() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.del(`/auth/sessions/${id}`),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.sessions }),
  });
}

// ── Dashboard ──────────────────────────────────────────────────────────────

export function useDashboard() {
  return useQuery({
    queryKey: keys.dashboard,
    queryFn: () => api.get<Dashboard>("/dashboard"),
    refetchInterval: 30_000,
  });
}

// ── Projects ───────────────────────────────────────────────────────────────

export function useProjects(includeArchived = false) {
  return useQuery({
    queryKey: keys.projects(includeArchived),
    queryFn: () =>
      api
        .get<{ projects: Project[] }>("/projects", { include_archived: includeArchived })
        .then((r) => r.projects),
    refetchInterval: 60_000,
  });
}

export function useProject(key: string | undefined) {
  return useQuery({
    queryKey: keys.project(key ?? ""),
    queryFn: () => api.get<ProjectDetail>(`/projects/${encodeURIComponent(key!)}`),
    enabled: !!key,
    refetchInterval: 30_000,
  });
}

export function useProjectActivity(key: string, limit = 50) {
  return useQuery({
    queryKey: keys.projectActivity(key),
    queryFn: () =>
      api
        .get<{ activity: Activity[] }>(`/projects/${encodeURIComponent(key)}/activity`, { limit })
        .then((r) => r.activity),
    refetchInterval: 60_000,
  });
}

export function useCreateProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ProjectInput) => api.post<ProjectDetail>("/projects", body),
    onSuccess: () => invalidateProjectViews(qc),
  });
}

export function useUpdateProject(key: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ProjectInput) =>
      api.patch<ProjectDetail>(`/projects/${encodeURIComponent(key)}`, body),
    onSuccess: (project) => {
      qc.setQueryData(keys.project(key), project);
      invalidateProjectViews(qc);
      void qc.invalidateQueries({ queryKey: ["tasks"] });
    },
  });
}

export function useDeleteProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (key: string) => api.del(`/projects/${encodeURIComponent(key)}`),
    onSuccess: () => {
      invalidateProjectViews(qc);
      void qc.invalidateQueries({ queryKey: ["tasks"] });
    },
  });
}

export function useSetProjectServers(key: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (servers: { server_id: number; role: string }[]) =>
      api
        .put<{ servers: ProjectServer[] }>(`/projects/${encodeURIComponent(key)}/servers`, { servers })
        .then((r) => r.servers),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.project(key) });
      void qc.invalidateQueries({ queryKey: keys.servers });
    },
  });
}

export function useSaveRepo(projectKey: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: RepoInput & { id?: number }) =>
      id
        ? api.patch<Repo>(`/repos/${id}`, body)
        : api.post<Repo>(`/projects/${encodeURIComponent(projectKey)}/repos`, body),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.project(projectKey) }),
  });
}

export function useProjectFiles(key: string) {
  return useQuery({
    queryKey: keys.projectFiles(key),
    queryFn: () =>
      api.get<{ files: ProjectFile[] }>(`/projects/${encodeURIComponent(key)}/files`).then((r) => r.files),
  });
}

/** The download URL: a plain link, so the browser streams it with the session cookie. */
export const projectFileUrl = (key: string, name: string) =>
  `/api/projects/${encodeURIComponent(key)}/files/${encodeURIComponent(name)}`;

export function useUploadProjectFile(key: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (file: File) => {
      const form = new FormData();
      form.append("file", file, file.name);
      return api.upload<{ file: ProjectFile }>(`/projects/${encodeURIComponent(key)}/files`, form).then((r) => r.file);
    },
    onSettled: () => void qc.invalidateQueries({ queryKey: keys.projectFiles(key) }),
  });
}

export function useDeleteProjectFile(key: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api.del(`/projects/${encodeURIComponent(key)}/files/${encodeURIComponent(name)}`),
    onSuccess: (_d, name) => {
      qc.setQueryData<ProjectFile[]>(keys.projectFiles(key), (old) => old?.filter((f) => f.name !== name));
      void qc.invalidateQueries({ queryKey: keys.projectFiles(key) });
    },
  });
}

export function useDeleteRepo(projectKey: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.del(`/repos/${id}`),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.project(projectKey) }),
  });
}

// ── Endpoints ──────────────────────────────────────────────────────────────

export function useEndpoints() {
  return useQuery({
    queryKey: keys.endpoints,
    queryFn: () => api.get<{ endpoints: Endpoint[] }>("/endpoints").then((r) => r.endpoints),
    refetchInterval: 30_000,
  });
}

export function useEndpointChecks(id: number, hours = 24) {
  return useQuery({
    queryKey: keys.endpointChecks(id, hours),
    queryFn: () =>
      api.get<{ checks: EndpointCheck[] }>(`/endpoints/${id}/checks`, { hours }).then((r) => r.checks),
    refetchInterval: 120_000,
  });
}

function invalidateEndpointViews(qc: QueryClient) {
  void qc.invalidateQueries({ queryKey: keys.endpoints });
  void qc.invalidateQueries({ queryKey: ["endpoint-checks"] });
  invalidateProjectViews(qc);
}

export function useSaveEndpoint(projectKey?: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: EndpointInput & { id?: number }) =>
      id
        ? api.patch<Endpoint>(`/endpoints/${id}`, body)
        : api.post<Endpoint>(`/projects/${encodeURIComponent(projectKey!)}/endpoints`, body),
    onSuccess: () => invalidateEndpointViews(qc),
  });
}

export function useDeleteEndpoint() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.del(`/endpoints/${id}`),
    onSuccess: () => invalidateEndpointViews(qc),
  });
}

export function useCheckEndpoint() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.post<Endpoint>(`/endpoints/${id}/check`),
    onSuccess: () => invalidateEndpointViews(qc),
  });
}

// ── Servers ────────────────────────────────────────────────────────────────

export function useServers() {
  return useQuery({
    queryKey: keys.servers,
    queryFn: () => api.get<{ servers: Server[] }>("/servers").then((r) => r.servers),
  });
}

export function useSaveServer() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: ServerInput & { id?: number }) =>
      id ? api.patch<Server>(`/servers/${id}`, body) : api.post<Server>("/servers", body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.servers });
      void qc.invalidateQueries({ queryKey: ["project"] });
    },
  });
}

export function useDeleteServer() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.del(`/servers/${id}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.servers });
      void qc.invalidateQueries({ queryKey: ["project"] });
    },
  });
}

// ── Tasks ──────────────────────────────────────────────────────────────────

export function useTasks(filters: TaskFilters, options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: keys.tasks(filters),
    queryFn: () =>
      api
        .get<{ tasks: Task[] }>("/tasks", {
          project: filters.project,
          status: filters.status,
          focus: filters.focus,
          q: filters.q,
          priority: filters.priority,
          type: filters.type,
          label: filters.label,
          overdue: filters.overdue,
          open: filters.open,
          limit: filters.limit,
        })
        .then((r) => r.tasks),
    placeholderData: keepPreviousData,
    enabled: options.enabled ?? true,
    refetchInterval: 60_000,
  });
}

export function useTask(id: number | null) {
  return useQuery({
    queryKey: keys.task(id ?? 0),
    queryFn: () => api.get<TaskDetail>(`/tasks/${id}`),
    enabled: id !== null && id > 0,
  });
}

export function useCreateTask() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: TaskInput & { project_key: string; title: string }) => api.post<Task>("/tasks", body),
    onSuccess: () => invalidateTaskViews(qc),
  });
}

/** Apply a partial change to every cached copy of a task (lists + detail). */
function patchTaskCaches(qc: QueryClient, id: number, patch: Partial<Task>) {
  qc.setQueriesData<Task[]>({ queryKey: ["tasks"] }, (old) =>
    old?.map((t) => (t.id === id ? { ...t, ...patch } : t)),
  );
  qc.setQueryData<TaskDetail>(keys.task(id), (old) => (old ? { ...old, ...patch } : old));
  qc.setQueryData<Dashboard>(keys.dashboard, (old) =>
    old
      ? {
          ...old,
          focus: old.focus
            .map((t) => (t.id === id ? { ...t, ...patch } : t))
            .filter((t) => t.focus && t.status !== "done"),
          overdue: old.overdue.map((t) => (t.id === id ? { ...t, ...patch } : t)).filter((t) => t.status !== "done"),
        }
      : old,
  );
}

export function useUpdateTask() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: TaskInput & { id: number }) => api.patch<Task>(`/tasks/${id}`, body),
    // Optimistic: kanban drags, focus stars and complete ticks must feel
    // instant. The server's answer replaces the guess on settle.
    onMutate: async ({ id, project_key, ...body }) => {
      await qc.cancelQueries({ queryKey: ["tasks"] });
      const snapshot = qc.getQueriesData({ queryKey: ["tasks"] });
      const detail = qc.getQueryData(keys.task(id));
      const dashboard = qc.getQueryData(keys.dashboard);
      const patch: Partial<Task> = { ...body };
      if (project_key) patch.project_key = project_key;
      if (body.status === "done") patch.completed_at = new Date().toISOString();
      else if (body.status) patch.completed_at = null;
      patchTaskCaches(qc, id, patch);
      return { snapshot, detail, dashboard };
    },
    onError: (_err, { id }, context) => {
      context?.snapshot.forEach(([key, data]) => qc.setQueryData(key, data));
      if (context?.detail) qc.setQueryData(keys.task(id), context.detail);
      if (context?.dashboard) qc.setQueryData(keys.dashboard, context.dashboard);
    },
    onSuccess: (task) => patchTaskCaches(qc, task.id, task),
    onSettled: (_data, _err, { id }) => invalidateTaskViews(qc, id),
  });
}

export function useDeleteTask() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.del(`/tasks/${id}`),
    onSuccess: (_d, id) => {
      qc.setQueriesData<Task[]>({ queryKey: ["tasks"] }, (old) => old?.filter((t) => t.id !== id));
      qc.removeQueries({ queryKey: keys.task(id) });
      invalidateTaskViews(qc);
    },
  });
}

export function useAddComment(taskId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: string) => api.post<Comment>(`/tasks/${taskId}/comments`, { body }),
    onSuccess: () => invalidateTaskViews(qc, taskId),
  });
}

export function useDeleteComment(taskId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.del(`/comments/${id}`),
    onSuccess: () => invalidateTaskViews(qc, taskId),
  });
}

// ── Agents ─────────────────────────────────────────────────────────────────

export function useRunners(refetchInterval: number | false = 10_000) {
  return useQuery({
    queryKey: keys.runners,
    queryFn: () => api.get<{ runners: Runner[] }>("/runners").then((r) => r.runners),
    refetchInterval,
  });
}

/** Adds a machine and returns its first pairing code (15 minutes, single use). */
export function useCreateRunner() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { name: string; role?: RunnerRole }) =>
      api.post<{ runner: Runner; pairing: Pairing }>("/runners", body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.runners });
      void qc.invalidateQueries({ queryKey: keys.dashboard });
    },
  });
}

/** A new pairing code for an existing machine; its current token works until the code is used. */
export function usePairRunner() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.post<{ runner: Runner; pairing: Pairing }>(`/runners/${id}/pair`),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.runners }),
  });
}

export function useRotateRunner() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.post<{ runner: Runner; token: string }>(`/runners/${id}/rotate`),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.runners }),
  });
}

/** Making one runner master demotes the previous master to worker (server-side). */
export function useSetRunnerRole() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, role }: { id: number; role: RunnerRole }) => api.patch<Runner>(`/runners/${id}`, { role }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.runners });
      void qc.invalidateQueries({ queryKey: keys.terminalHosts });
      void qc.invalidateQueries({ queryKey: keys.dashboard });
    },
  });
}

export function useDeleteRunner() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.del(`/runners/${id}`),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.runners }),
  });
}

export function useRuns(filters: RunFilters, refetchInterval: number | false = 8_000) {
  return useQuery({
    queryKey: keys.runs(filters),
    queryFn: () =>
      api
        .get<{ runs: Run[] }>("/runs", {
          project: filters.project,
          task: filters.task,
          status: filters.status,
          limit: filters.limit,
        })
        .then((r) => r.runs),
    placeholderData: keepPreviousData,
    refetchInterval,
  });
}

/** `live`: poll every 3 s while the run is queued or running (the run page polls its events instead). */
export function useRun(id: number, { live = false }: { live?: boolean } = {}) {
  return useQuery({
    queryKey: keys.run(id),
    queryFn: () => api.get<Run>(`/runs/${id}`),
    enabled: id > 0,
    refetchInterval: live ? (query) => (isActiveRun(query.state.data) ? 3_000 : false) : undefined,
  });
}

export function isActiveRun(run: Pick<Run, "status"> | undefined | null): boolean {
  return !!run && (run.status === "queued" || run.status === "running");
}

type RunEventsData = { events: RunEvent[]; run: Run };

/**
 * Incremental log polling. Each fetch asks only for events after the last seq
 * already held and appends them, so a long run costs one small request per
 * tick instead of re-downloading its whole transcript. Polls every 1.5 s while
 * the run is active and stops once it has finished.
 */
export function useRunEvents(id: number) {
  const qc = useQueryClient();
  return useQuery({
    queryKey: keys.runEvents(id),
    queryFn: async ({ signal }) => {
      let prev = qc.getQueryData<RunEventsData>(keys.runEvents(id));
      // Drain: a finished run with a big transcript arrives in pages of 500.
      for (let page = 0; page < 20; page++) {
        const after = prev?.events.length ? prev.events[prev.events.length - 1].seq : 0;
        const res = await api.get<RunEventsData>(`/runs/${id}/events`, { after, limit: 500 }, signal);
        const merged: RunEventsData = {
          run: res.run,
          events: prev ? [...prev.events, ...res.events.filter((e) => e.seq > after)] : res.events,
        };
        prev = merged;
        if (res.events.length < 500) break;
      }
      if (prev) qc.setQueryData(keys.run(id), prev.run);
      return prev!;
    },
    refetchInterval: (query) => (isActiveRun(query.state.data?.run) || !query.state.data ? 1500 : false),
    structuralSharing: false,
  });
}

export function useCreateRun() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: RunInput) => api.post<Run>("/runs", body),
    onSuccess: (run) => {
      void qc.invalidateQueries({ queryKey: ["runs"] });
      void qc.invalidateQueries({ queryKey: keys.dashboard });
      void qc.invalidateQueries({ queryKey: keys.runners });
      if (run.task_id) void qc.invalidateQueries({ queryKey: keys.task(run.task_id) });
    },
  });
}

export function useCancelRun() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.post<Run>(`/runs/${id}/cancel`),
    onSuccess: (run) => {
      qc.setQueryData(keys.run(run.id), run);
      void qc.invalidateQueries({ queryKey: keys.runEvents(run.id) });
      void qc.invalidateQueries({ queryKey: ["runs"] });
      void qc.invalidateQueries({ queryKey: keys.dashboard });
    },
  });
}

// ── Vault ──────────────────────────────────────────────────────────────────
//
// Revealed secret values never go through React Query (no useQuery, no
// useMutation — both keep results in a cache that outlives the component).
// Components call `revealVaultItem` and hold the answer in local state only.

export function useVault(filters: VaultFilters) {
  return useQuery({
    queryKey: keys.vault(filters),
    queryFn: () => api.get<{ items: VaultItem[]; available: boolean }>("/vault", filters),
    placeholderData: keepPreviousData,
  });
}

export function useVaultItem(id: number | null) {
  return useQuery({
    queryKey: keys.vaultItem(id ?? 0),
    queryFn: () => api.get<VaultItem>(`/vault/${id}`),
    enabled: id !== null && id > 0,
  });
}

export function useVaultAudit(limit = 100, enabled = true) {
  return useQuery({
    queryKey: keys.vaultAudit,
    queryFn: () => api.get<{ events: VaultAudit[] }>("/vault/audit", { limit }).then((r) => r.events),
    enabled,
  });
}

function invalidateVault(qc: QueryClient) {
  void qc.invalidateQueries({ queryKey: ["vault"] });
  void qc.invalidateQueries({ queryKey: ["vault-item"] });
  void qc.invalidateQueries({ queryKey: keys.vaultAudit });
}

export function useSaveVaultItem() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: VaultInput & { id?: number }) =>
      id ? api.patch<VaultItem>(`/vault/${id}`, body) : api.post<VaultItem>("/vault", body),
    onSuccess: () => invalidateVault(qc),
  });
}

export function useDeleteVaultItem() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.del(`/vault/${id}`),
    onSuccess: (_d, id) => {
      qc.removeQueries({ queryKey: keys.vaultItem(id) });
      invalidateVault(qc);
    },
  });
}

/** Secret values for one item. Keep the result in component state only. */
export function revealVaultItem(id: number): Promise<Record<string, string>> {
  return api.post<{ secret: Record<string, string> }>(`/vault/${id}/reveal`).then((r) => r.secret ?? {});
}

// ── Monitoring ─────────────────────────────────────────────────────────────

export function useMonitoring(enabled = true) {
  return useQuery({
    queryKey: keys.monitoring,
    queryFn: () => api.get<Monitoring>("/monitoring"),
    refetchInterval: 30_000,
    staleTime: 25_000,
    enabled,
  });
}

/** Bypass the API's 30 s cache and replace what the page shows. */
export function useRefreshMonitoring() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.get<Monitoring>("/monitoring", { fresh: true }),
    onSuccess: (m) => qc.setQueryData(keys.monitoring, m),
  });
}

// ── Daily check-up ─────────────────────────────────────────────────────────

export function useCheckups(limit = 30) {
  return useQuery({
    queryKey: keys.checkups,
    queryFn: () => api.get<{ checkups: CheckupSummary[] }>("/checkups", { limit }).then((r) => r.checkups),
  });
}

/** The latest check-up, or null when none has ever run (the API answers 404). */
export function useLatestCheckup(enabled = true) {
  return useQuery({
    queryKey: keys.checkupLatest,
    queryFn: () =>
      api.get<Checkup>("/checkups/latest").catch((err) => {
        if (err instanceof ApiError && err.status === 404) return null;
        throw err;
      }),
    enabled,
    refetchInterval: 60_000,
  });
}

export function useCheckup(id: number | null) {
  return useQuery({
    queryKey: keys.checkup(id ?? 0),
    queryFn: () => api.get<Checkup>(`/checkups/${id}`),
    enabled: id !== null && id > 0,
  });
}

function storeCheckup(qc: QueryClient, checkup: Checkup) {
  qc.setQueryData(keys.checkup(checkup.id), checkup);
  qc.setQueryData<Checkup | null>(keys.checkupLatest, (old) => (!old || old.id === checkup.id ? checkup : old));
  void qc.invalidateQueries({ queryKey: keys.checkups });
  void qc.invalidateQueries({ queryKey: keys.dashboard });
}

export function useRunCheckup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<Checkup>("/checkups"),
    onSuccess: (checkup) => {
      qc.setQueryData(keys.checkupLatest, checkup);
      storeCheckup(qc, checkup);
    },
  });
}

export function useCheckItemDone(checkupId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ key, done }: { key: string; done: boolean }) =>
      api.patch<Checkup>(`/checkups/${checkupId}/items/${encodeURIComponent(key)}`, { done }),
    onMutate: ({ key, done }) => {
      const patch = (c: Checkup | null | undefined) =>
        c && c.id === checkupId
          ? {
              ...c,
              items: c.items.map((i) => (i.key === key ? { ...i, done } : i)),
              actions_done: c.actions_done + (done ? 1 : -1),
            }
          : c;
      qc.setQueryData<Checkup | null>(keys.checkup(checkupId), patch);
      qc.setQueryData<Checkup | null>(keys.checkupLatest, patch);
    },
    onSuccess: (checkup) => storeCheckup(qc, checkup),
    onError: () => {
      void qc.invalidateQueries({ queryKey: keys.checkup(checkupId) });
      void qc.invalidateQueries({ queryKey: keys.checkupLatest });
    },
  });
}

export function useCheckItemTask(checkupId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ key, project_key }: { key: string; project_key?: string }) =>
      api.post<{ task: Task; checkup: Checkup }>(
        `/checkups/${checkupId}/items/${encodeURIComponent(key)}/task`,
        project_key ? { project_key } : {},
      ),
    onSuccess: ({ checkup }) => {
      storeCheckup(qc, checkup);
      invalidateTaskViews(qc);
    },
  });
}

// ── Terminal ───────────────────────────────────────────────────────────────
//
// Terminal routes need elevation. Polling uses `getQuiet`, so an expired
// elevation shows a "locked" state instead of a password dialog popping up
// every five seconds; pages offer an explicit unlock.

export function useTerminalHosts(options: { enabled?: boolean; refetchInterval?: number | false } = {}) {
  return useQuery({
    queryKey: keys.terminalHosts,
    queryFn: ({ signal }) => api.getQuiet<TerminalHosts>("/terminal/hosts", undefined, signal),
    // Stop polling while locked; unlocking invalidates the query and it resumes.
    refetchInterval: (query) =>
      isElevationError(query.state.error) ? false : (options.refetchInterval ?? 5_000),
    enabled: options.enabled ?? true,
    retry: false,
  });
}

const sessionPath = (runnerId: number, name: string) => `/terminal/${runnerId}/sessions/${encodeURIComponent(name)}`;

export function useCreateSession() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ runnerId, ...body }: NewSessionInput & { runnerId: number }) =>
      api.post<{ session: string }>(`/terminal/${runnerId}/sessions`, body),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.terminalHosts }),
  });
}

export function useKillSession() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ runnerId, name }: { runnerId: number; name: string }) => api.del(sessionPath(runnerId, name)),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.terminalHosts }),
  });
}

/** The visible screen + scrollback as plain text, refreshed every 3 s while open. `windowIndex` targets one window. */
export function useSessionCapture(runnerId: number, name: string, windowIndex?: number | null, lines = 200, enabled = true) {
  return useQuery({
    queryKey: [...keys.terminalCapture(runnerId, name), windowIndex ?? null],
    queryFn: ({ signal }) =>
      api
        .get<{ text: string }>(`${sessionPath(runnerId, name)}/capture`, { lines, window: windowIndex ?? undefined }, signal)
        .then((r) => r.text),
    refetchInterval: 3_000,
    enabled,
    retry: false,
    gcTime: 0, // screen contents are not worth keeping once the peek closes
  });
}

export function useSendKeys(runnerId: number, name: string, windowIndex?: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { text: string; enter: boolean }) =>
      api.post<void>(
        `${sessionPath(runnerId, name)}/keys`,
        windowIndex === null || windowIndex === undefined ? body : { ...body, window: windowIndex },
      ),
    // The screen changes a moment after the keys land.
    onSuccess: () =>
      void setTimeout(() => void qc.invalidateQueries({ queryKey: keys.terminalCapture(runnerId, name) }), 400),
  });
}

// ── VS Code ────────────────────────────────────────────────────────────────

export function useCodeStatus(enabled = true) {
  return useQuery({
    queryKey: keys.codeStatus,
    queryFn: ({ signal }) => api.getQuiet<CodeStatus>("/code/status", undefined, signal),
    refetchInterval: 60_000,
    staleTime: 30_000,
    retry: false,
    enabled,
  });
}

/** POST /code/open — needs elevation; the client's confirm-it's-you retry covers it. */
export function useOpenCode() {
  return useMutation({
    mutationFn: (body: CodeOpenInput) => api.post<CodeOpenResult>("/code/open", body),
  });
}

// ── Security log & system facts ────────────────────────────────────────────

export function useSecurityLog(kind = "", limit = 100) {
  // Explicit type: with a destructured `signal` the placeholderData overload
  // fails to infer TData and leaks the keepPreviousData function type.
  return useQuery<SecurityEvent[]>({
    queryKey: keys.security(kind, limit),
    queryFn: ({ signal }) =>
      api.get<{ events: SecurityEvent[] }>("/auth/security", { kind: kind || undefined, limit }, signal).then((r) => r.events),
    placeholderData: keepPreviousData,
  });
}

export function useRevokeOtherSessions() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<{ revoked: number }>("/auth/sessions/revoke-others"),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.sessions });
      void qc.invalidateQueries({ queryKey: ["security"] });
    },
  });
}

export function useSystemFacts(enabled = true) {
  return useQuery({
    queryKey: keys.system,
    queryFn: ({ signal }) => api.get<SystemFacts>("/system", undefined, signal),
    refetchInterval: 60_000,
    enabled,
  });
}

// ── Agent engine ──────────────────────────────────────────────────────────

export function useEngine() {
  return useQuery({ queryKey: ["engine"], queryFn: () => api.get<EngineStatus>("/engine") });
}

export function useUpdateEngine() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (patch: Partial<EngineSettings>) => api.patch<EngineStatus>("/engine", patch),
    onSuccess: (s) => qc.setQueryData(["engine"], s),
  });
}

/** Stores the DeepSeek key agent runs use (needs a recent password confirmation). */
export function useSetDeepseekKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (apiKey: string) => api.put<EngineStatus>("/engine/key", { api_key: apiKey }),
    onSuccess: (s) => {
      qc.setQueryData(["engine"], s);
      void qc.invalidateQueries({ queryKey: ["vault"] });
    },
  });
}

// ── Jev (token saving) ────────────────────────────────────────────────────

export function useJev() {
  return useQuery({ queryKey: ["jev"], queryFn: () => api.get<JevStatus>("/jev") });
}

export function useUpdateJev() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (patch: Partial<JevSettings>) => api.patch<JevStatus>("/jev", patch),
    onSuccess: (s) => qc.setQueryData(["jev"], s),
  });
}

/** Stores the TypeSafe key in the vault (needs a recent password confirmation). */
export function useSetJevKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (apiKey: string) => api.put<JevStatus>("/jev/key", { api_key: apiKey }),
    onSuccess: (s) => {
      qc.setQueryData(["jev"], s);
      void qc.invalidateQueries({ queryKey: ["vault"] });
    },
  });
}

export function useTestJev() {
  return useMutation({
    mutationFn: () => api.post<{ ok: boolean; ms?: number; error?: string }>("/jev/test"),
  });
}

// ── Assistant ─────────────────────────────────────────────────────────────
//
// The agent loop runs on the server. The page posts a message, then polls the
// chat for new messages (`after` the last seq it holds) while `chat.busy`,
// the same incremental pattern as a run's transcript.

export function useAssistant() {
  return useQuery({ queryKey: keys.assistant, queryFn: () => api.get<AssistantStatus>("/assistant") });
}

export function useUpdateAssistant() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (patch: Partial<AssistantSettings>) => api.patch<AssistantStatus>("/assistant", patch),
    onSuccess: (s) => qc.setQueryData(keys.assistant, s),
  });
}

/** Stores the provider key in the vault (needs a recent password confirmation). */
export function useSetAssistantKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (apiKey: string) => api.put<AssistantStatus>("/assistant/key", { api_key: apiKey }),
    onSuccess: (s) => {
      qc.setQueryData(keys.assistant, s);
      void qc.invalidateQueries({ queryKey: ["vault"] });
    },
  });
}

/** Put a chat's latest state wherever it is cached: the list and its thread. */
function storeChat(qc: QueryClient, chat: Chat) {
  qc.setQueryData<Chat[]>(keys.chats, (old) => (old ? upsertChat(old, chat) : old));
  qc.setQueryData<ChatThread>(keys.chat(chat.id), (old) => (old ? { ...old, chat } : old));
}

function storeThread(qc: QueryClient, thread: ChatThread) {
  qc.setQueryData<ChatThread>(keys.chat(thread.chat.id), (old) => ({
    chat: thread.chat,
    messages: mergeMessages(old?.messages ?? [], thread.messages),
  }));
  qc.setQueryData<Chat[]>(keys.chats, (old) => (old ? upsertChat(old, thread.chat) : old));
}

export function useChats() {
  return useQuery({
    queryKey: keys.chats,
    queryFn: ({ signal }) => api.get<{ chats: Chat[] }>("/chats", undefined, signal).then((r) => r.chats),
    // A busy chat may get its title from the agent; otherwise nothing changes by itself.
    refetchInterval: (query) => (query.state.data?.some((c) => c.busy) ? 5_000 : false),
  });
}

export function useChat(id: number | null) {
  const qc = useQueryClient();
  return useQuery({
    queryKey: keys.chat(id ?? 0),
    queryFn: async ({ signal }) => {
      const prev = qc.getQueryData<ChatThread>(keys.chat(id!));
      const after = lastSeq(prev?.messages);
      const res = await api.get<ChatThread>(`/chats/${id}`, { after: after || undefined }, signal);
      qc.setQueryData<Chat[]>(keys.chats, (old) => (old ? upsertChat(old, res.chat) : old));
      return { chat: res.chat, messages: mergeMessages(prev?.messages ?? [], res.messages) };
    },
    enabled: id !== null && id > 0,
    refetchInterval: (query) => (query.state.data?.chat.busy ? 1500 : false),
    structuralSharing: false,
  });
}

// 503 assistant_off: switched off (or its key removed) since the page loaded.
function onAssistantError(qc: QueryClient, err: unknown) {
  if (err instanceof ApiError && err.code === "assistant_off") void qc.invalidateQueries({ queryKey: keys.assistant });
}

/** Creates the chat with its first message; the agent starts on it right away. */
export function useCreateChat() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (content: string) => api.post<ChatThread>("/chats", { content }),
    onSuccess: (thread) => {
      storeThread(qc, thread);
      void qc.invalidateQueries({ queryKey: keys.chats });
    },
    onError: (err) => onAssistantError(qc, err),
  });
}

/** 409 `busy` while the agent is still on the previous turn. */
export function useSendMessage(chatId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (content: string) => api.post<ChatThread>(`/chats/${chatId}/messages`, { content }),
    onSuccess: (thread) => storeThread(qc, thread),
    onError: (err) => {
      onAssistantError(qc, err);
      // Busy after all: pick the running turn back up.
      if (err instanceof ApiError && err.code === "busy") void qc.invalidateQueries({ queryKey: keys.chat(chatId) });
    },
  });
}

export function useStopChat(chatId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<{ chat: Chat }>(`/chats/${chatId}/stop`).then((r) => r.chat),
    onSuccess: (chat) => {
      storeChat(qc, chat);
      // Whatever the turn wrote before it stopped.
      void qc.invalidateQueries({ queryKey: keys.chat(chatId) });
    },
  });
}

export function useRenameChat(chatId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (title: string) => api.patch<{ chat: Chat }>(`/chats/${chatId}`, { title }).then((r) => r.chat),
    onSuccess: (chat) => storeChat(qc, chat),
  });
}

export function useDeleteChat() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.del(`/chats/${id}`),
    onSuccess: (_d, id) => {
      qc.setQueryData<Chat[]>(keys.chats, (old) => old?.filter((c) => c.id !== id));
      qc.removeQueries({ queryKey: keys.chat(id) });
      void qc.invalidateQueries({ queryKey: keys.chats });
    },
  });
}
