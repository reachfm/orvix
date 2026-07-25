import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle, CheckCircle2, Download, History as HistoryIcon,
  Loader2, Lock, RefreshCw, RotateCcw, ShieldCheck, XCircle,
} from "lucide-react";
import { api } from "../api";

// ── Types (mirror internal/selfupdate/types.go + protocol.go) ─────────
// Orvix ships as ONE signed bundle, not per-module packages, so this page
// exposes exactly one system-wide update action rather than per-module
// buttons. The backend Job model is a flat set of scalar fields (phase,
// progress_percent, failure_code/message, rollback_result) — it does not
// currently return a structured list of named preflight checks or a
// per-job event log, so this UI reports the real job phase/progress and
// failure message rather than inventing a checklist the API doesn't send.

type Phase =
  | "queued" | "checking" | "downloading" | "verifying" | "preflight"
  | "backing_up" | "stopping_service" | "migrating" | "replacing_runtime"
  | "restarting" | "health_check" | "completed" | "failed"
  | "rolling_back" | "rolled_back";

interface Job {
  id: string;
  kind: "install" | "rollback";
  idempotency_key: string;
  requested_version: string;
  initiated_by: string;
  phase: Phase;
  progress_percent: number;
  created_at: string;
  updated_at: string;
  artifact_sha256?: string;
  artifact_version?: string;
  artifact_commit?: string;
  rollback_snapshot_id?: string;
  failure_code?: string;
  failure_message?: string;
  rollback_result?: string;
}

interface ReleaseInfo {
  tag: string;
  version: string;
  channel: string;
  published_at: string;
  prerelease: boolean;
  asset_name: string;
}

interface RollbackSnapshot {
  id: string;
  source_version: string;
  source_commit: string;
  checksum_sha256: string;
  verified: boolean;
  created_at: string;
  last_known_good: boolean;
  retained: boolean;
}

const TERMINAL_PHASES: Phase[] = ["completed", "failed", "rolled_back"];

const PHASE_LABELS: Record<Phase, string> = {
  queued: "Queued",
  checking: "Checking release",
  downloading: "Downloading bundle",
  verifying: "Verifying signature",
  preflight: "Running preflight checks",
  backing_up: "Backing up current install",
  stopping_service: "Stopping service",
  migrating: "Running migrations",
  replacing_runtime: "Replacing runtime",
  restarting: "Restarting service",
  health_check: "Post-update health check",
  completed: "Completed",
  failed: "Failed",
  rolling_back: "Rolling back",
  rolled_back: "Rolled back",
};

// Client-side-only freshness window for a passed preflight before Install
// is re-gated. The backend does not send an expiry, so this is a UX
// safety margin, not a security control — the backend re-validates
// everything server-side regardless.
const PREFLIGHT_FRESHNESS_MS = 5 * 60 * 1000;

function fmtDate(s?: string): string {
  if (!s) return "-";
  const d = new Date(s);
  if (isNaN(d.getTime())) return s;
  return d.toLocaleString();
}

function genIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  return `key-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function StatusBadge({ phase }: { phase: Phase }) {
  const map: Record<string, string> = {
    completed: "bg-[#34D399]/10 text-[#34D399]",
    rolled_back: "bg-[#FBBF24]/10 text-[#FBBF24]",
    failed: "bg-[#F87171]/10 text-[#F87171]",
  };
  const cls = map[phase] || "bg-[#4F7CFF]/10 text-[#4F7CFF]";
  return <span className={`px-2 py-1 text-xs rounded-full ${cls}`}>{PHASE_LABELS[phase] || phase}</span>;
}

function ReauthModal({
  title, onConfirm, onCancel, pending, error,
}: {
  title: string;
  onConfirm: (password: string) => void;
  onCancel: () => void;
  pending: boolean;
  error?: string | null;
}) {
  const [password, setPassword] = useState("");
  return (
    <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50">
      <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-6 w-full max-w-sm">
        <div className="flex items-center gap-2 mb-3">
          <Lock className="w-5 h-5 text-[#4F7CFF]" />
          <h3 className="text-white font-medium">{title}</h3>
        </div>
        <p className="text-sm text-[#8B92A8] mb-3">
          This is an irreversible system action. Re-enter your password to confirm.
        </p>
        <input
          type="password"
          autoFocus
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter" && password) onConfirm(password); }}
          placeholder="Current password"
          className="w-full px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm mb-3"
        />
        {error && <p className="text-[#F87171] text-sm mb-3">{error}</p>}
        <div className="flex gap-2 justify-end">
          <button onClick={onCancel} className="px-3 py-2 text-sm text-[#8B92A8] hover:text-white">
            Cancel
          </button>
          <button
            onClick={() => onConfirm(password)}
            disabled={!password || pending}
            className="flex items-center gap-2 bg-[#4F7CFF] text-white rounded px-4 py-2 text-sm hover:bg-[#3D6AE8] disabled:opacity-50"
          >
            {pending ? <Loader2 className="w-4 h-4 animate-spin" /> : <Lock className="w-4 h-4" />}
            Confirm
          </button>
        </div>
      </div>
    </div>
  );
}

export default function UpdatesPage() {
  const queryClient = useQueryClient();

  const [channel, setChannel] = useState<"stable" | "prerelease">("stable");
  const [selectedVersion, setSelectedVersion] = useState<string | null>(null);
  const [preflightJob, setPreflightJob] = useState<Job | null>(null);
  const [activeJobId, setActiveJobId] = useState<string | null>(null);
  const [reauthAction, setReauthAction] = useState<null | "install" | "rollback">(null);
  const [rollbackTarget, setRollbackTarget] = useState<string | null>(null);
  const [installIdempotencyKey] = useState(genIdempotencyKey);
  const [rollbackIdempotencyKey, setRollbackIdempotencyKey] = useState(genIdempotencyKey);

  // ── Reconnect to any job already in progress on page load / refresh ──
  const statusQuery = useQuery({
    queryKey: ["updatesStatus"],
    queryFn: api.getUpdatesStatus,
  });

  useEffect(() => {
    const job: Job | null = statusQuery.data?.job ?? null;
    if (job) {
      setActiveJobId(job.id);
    }
  }, [statusQuery.data]);

  // ── Poll the active job at ~2s while it's running ─────────────────
  const jobQuery = useQuery({
    queryKey: ["updateJob", activeJobId],
    queryFn: () => api.getUpdateJob(activeJobId as string),
    enabled: !!activeJobId,
    refetchInterval: (query) => {
      const job = (query.state.data as any)?.job as Job | undefined;
      if (!job || TERMINAL_PHASES.includes(job.phase)) return false;
      return 2000;
    },
  });

  const activeJob: Job | undefined = jobQuery.data?.job;

  useEffect(() => {
    if (activeJob && TERMINAL_PHASES.includes(activeJob.phase)) {
      queryClient.invalidateQueries({ queryKey: ["updateHistory"] });
      queryClient.invalidateQueries({ queryKey: ["updateSnapshots"] });
    }
  }, [activeJob?.phase]); // eslint-disable-line react-hooks/exhaustive-deps

  const releasesQuery = useQuery({
    queryKey: ["updateReleases", channel],
    queryFn: () => api.listUpdateReleases(channel),
  });

  const historyQuery = useQuery({ queryKey: ["updateHistory"], queryFn: api.listUpdateHistory });
  const snapshotsQuery = useQuery({ queryKey: ["updateSnapshots"], queryFn: api.listUpdateSnapshots });

  const checkMutation = useMutation({
    mutationFn: () => api.checkForUpdates(channel),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["updateReleases", channel] });
      setPreflightJob(null);
    },
  });

  const preflightMutation = useMutation({
    mutationFn: (version: string) => api.runUpdatePreflight(version),
    onSuccess: (data) => setPreflightJob(data.job as Job),
  });

  const installMutation = useMutation({
    mutationFn: (password: string) =>
      api.installUpdate({
        password,
        idempotency_key: installIdempotencyKey,
        requested_version: selectedVersion as string,
        channel,
      }),
    onSuccess: (data) => {
      setReauthAction(null);
      setActiveJobId(data.job?.id ?? null);
    },
  });

  const rollbackMutation = useMutation({
    mutationFn: (password: string) =>
      api.rollbackUpdate({ password, idempotency_key: rollbackIdempotencyKey, target: rollbackTarget as string }),
    onSuccess: (data) => {
      setReauthAction(null);
      setRollbackTarget(null);
      setRollbackIdempotencyKey(genIdempotencyKey());
      setActiveJobId(data.job?.id ?? null);
    },
  });

  const releases: ReleaseInfo[] = releasesQuery.data?.releases || [];
  const history: Job[] = historyQuery.data?.history || [];
  const snapshots: RollbackSnapshot[] = snapshotsQuery.data?.snapshots || [];
  const currentJob: Job | null = statusQuery.data?.job ?? null;
  const currentVersion = currentJob?.artifact_version || currentJob?.requested_version || "unknown";

  const isBackendOffline = (err: unknown) =>
    err instanceof Error && /self-update is not available|unreachable/i.test(err.message);

  const preflightIsFresh =
    !!preflightJob &&
    preflightJob.phase === "completed" &&
    !preflightJob.failure_message &&
    Date.now() - new Date(preflightJob.updated_at).getTime() < PREFLIGHT_FRESHNESS_MS &&
    preflightJob.requested_version === selectedVersion;

  const jobRunning = !!activeJob && !TERMINAL_PHASES.includes(activeJob.phase);
  const canInstall = !!selectedVersion && preflightIsFresh && !jobRunning;

  if (statusQuery.isLoading) {
    return <p className="text-[#8B92A8]">Loading update status...</p>;
  }

  if (statusQuery.isError && isBackendOffline(statusQuery.error)) {
    return (
      <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-8 text-center">
        <AlertTriangle className="w-8 h-8 text-[#FBBF24] mx-auto mb-3" />
        <p className="text-[#E8EAF0] font-medium mb-1">Self-update is not available on this deployment.</p>
        <p className="text-[#8B92A8] text-sm">The update service is not configured or is currently offline.</p>
      </div>
    );
  }

  return (
    <div className="space-y-6 max-w-4xl">
      <h2 className="text-2xl font-semibold text-[#E8EAF0]">Updates</h2>

      {/* Active job banner (reconnected on load, or started this session) */}
      {jobRunning && activeJob && (
        <div className="bg-[#13161C] border border-[#4F7CFF]/40 rounded-xl p-5">
          <div className="flex items-center justify-between mb-2">
            <div className="flex items-center gap-2">
              <Loader2 className="w-4 h-4 text-[#4F7CFF] animate-spin" />
              <span className="text-[#E8EAF0] font-medium">
                {activeJob.kind === "rollback" ? "Rollback in progress" : "Update in progress"}
              </span>
            </div>
            <StatusBadge phase={activeJob.phase} />
          </div>
          <div className="w-full bg-[#0C0E12] rounded-full h-2 mb-2">
            <div
              className="bg-[#4F7CFF] h-2 rounded-full transition-all"
              style={{ width: `${Math.max(0, Math.min(100, activeJob.progress_percent))}%` }}
            />
          </div>
          <p className="text-xs text-[#8B92A8]">
            {activeJob.progress_percent}% — {PHASE_LABELS[activeJob.phase] || activeJob.phase} — job {activeJob.id}
          </p>
        </div>
      )}

      {/* Terminal result of the most recently tracked job */}
      {activeJob && TERMINAL_PHASES.includes(activeJob.phase) && (
        <div
          className={`rounded-xl p-5 border ${
            activeJob.phase === "completed"
              ? "bg-[#34D399]/5 border-[#34D399]/30"
              : "bg-[#F87171]/5 border-[#F87171]/30"
          }`}
        >
          <div className="flex items-center gap-2 mb-1">
            {activeJob.phase === "completed" ? (
              <CheckCircle2 className="w-5 h-5 text-[#34D399]" />
            ) : (
              <XCircle className="w-5 h-5 text-[#F87171]" />
            )}
            <span className="text-[#E8EAF0] font-medium">
              {activeJob.phase === "completed"
                ? "Update completed — post-update health check reported healthy"
                : activeJob.phase === "rolled_back"
                ? "Update failed and was rolled back"
                : "Update failed"}
            </span>
          </div>
          {activeJob.failure_message && (
            <p className="text-sm text-[#F87171] mt-1">{activeJob.failure_message}</p>
          )}
          {activeJob.rollback_result && (
            <p className="text-sm text-[#8B92A8] mt-1">Rollback outcome: {activeJob.rollback_result}</p>
          )}
          {activeJob.phase !== "completed" && (
            <button
              onClick={() => { setActiveJobId(null); checkMutation.mutate(); }}
              className="mt-3 flex items-center gap-2 text-sm text-[#4F7CFF] hover:underline"
            >
              <RefreshCw className="w-4 h-4" /> Retry
            </button>
          )}
        </div>
      )}

      {/* Current version */}
      <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-5">
        <h3 className="text-[#E8EAF0] font-medium mb-1">Current Version</h3>
        <p className="text-[#8B92A8] text-sm">
          Orvix is a single signed bundle — all modules ship and update together as one system version.
        </p>
        <p className="text-lg text-[#E8EAF0] mt-2 font-mono">v{currentVersion}</p>
      </div>

      {/* Channel + check */}
      <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-5">
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-[#E8EAF0] font-medium">Check for Updates</h3>
          <div className="flex items-center gap-2">
            <select
              value={channel}
              onChange={(e) => setChannel(e.target.value as "stable" | "prerelease")}
              className="bg-[#0C0E12] border border-[#2A2F3E] rounded px-2 py-1.5 text-sm text-[#E8EAF0]"
            >
              <option value="stable">Stable</option>
              <option value="prerelease">Prerelease</option>
            </select>
            <button
              onClick={() => checkMutation.mutate()}
              disabled={checkMutation.isPending}
              className="flex items-center gap-2 bg-[#4F7CFF] text-white rounded px-4 py-2 text-sm hover:bg-[#3D6AE8] disabled:opacity-50"
            >
              {checkMutation.isPending ? <Loader2 className="w-4 h-4 animate-spin" /> : <RefreshCw className="w-4 h-4" />}
              Check for Updates
            </button>
          </div>
        </div>

        {checkMutation.isError && !isBackendOffline(checkMutation.error) && (
          <p className="text-[#F87171] text-sm mb-3">
            {(checkMutation.error as Error).message || "Failed to check for updates."}
          </p>
        )}
        {checkMutation.isError && isBackendOffline(checkMutation.error) && (
          <p className="text-[#FBBF24] text-sm mb-3">The update service is currently unreachable.</p>
        )}

        {releasesQuery.isSuccess && releases.length === 0 && (
          <p className="text-[#8B92A8] text-sm">No update available — you're on the latest {channel} release.</p>
        )}

        {releases.length > 0 && (
          <div className="space-y-2">
            {releases.map((r) => (
              <label
                key={r.tag}
                className={`flex items-center justify-between p-3 rounded-lg border cursor-pointer ${
                  selectedVersion === r.version
                    ? "border-[#4F7CFF] bg-[#4F7CFF]/5"
                    : "border-[#2A2F3E] hover:bg-[#1A1E26]"
                }`}
              >
                <div className="flex items-center gap-3">
                  <input
                    type="radio"
                    name="release"
                    checked={selectedVersion === r.version}
                    onChange={() => { setSelectedVersion(r.version); setPreflightJob(null); }}
                  />
                  <div>
                    <p className="text-[#E8EAF0] text-sm font-mono">v{r.version} ({r.tag})</p>
                    <p className="text-xs text-[#8B92A8]">
                      {r.channel} · published {fmtDate(r.published_at)}
                      {r.prerelease ? " · prerelease" : ""}
                    </p>
                  </div>
                </div>
                <Download className="w-4 h-4 text-[#8B92A8]" />
              </label>
            ))}
            <p className="text-xs text-[#555D73] pt-1">
              Detailed release notes are not currently returned by the update service API; only tag, version,
              channel and publish date are shown.
            </p>
          </div>
        )}
      </div>

      {/* Preflight */}
      {selectedVersion && (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-5">
          <div className="flex items-center justify-between mb-3">
            <h3 className="text-[#E8EAF0] font-medium flex items-center gap-2">
              <ShieldCheck className="w-4 h-4 text-[#4F7CFF]" /> Preflight Checklist
            </h3>
            <button
              onClick={() => preflightMutation.mutate(selectedVersion)}
              disabled={preflightMutation.isPending || jobRunning}
              className="flex items-center gap-2 bg-[#222736] text-[#E8EAF0] rounded px-3 py-1.5 text-sm hover:bg-[#2A2F3E] disabled:opacity-50"
            >
              {preflightMutation.isPending ? <Loader2 className="w-4 h-4 animate-spin" /> : <ShieldCheck className="w-4 h-4" />}
              Run Preflight
            </button>
          </div>

          {preflightMutation.isError && (
            <p className="text-[#F87171] text-sm mb-2">
              {(preflightMutation.error as Error).message || "Preflight failed to run."}
            </p>
          )}

          {preflightJob && (
            <div className="space-y-2">
              <div className="flex items-center gap-2">
                {preflightJob.phase === "completed" && !preflightJob.failure_message ? (
                  <CheckCircle2 className="w-4 h-4 text-[#34D399]" />
                ) : preflightJob.failure_message ? (
                  <XCircle className="w-4 h-4 text-[#F87171]" />
                ) : (
                  <Loader2 className="w-4 h-4 text-[#4F7CFF] animate-spin" />
                )}
                <span className="text-sm text-[#E8EAF0]">
                  {preflightJob.failure_message
                    ? "Preflight failed — blocking, cannot install"
                    : preflightJob.phase === "completed"
                    ? "Preflight passed"
                    : `Preflight running (${PHASE_LABELS[preflightJob.phase] || preflightJob.phase})`}
                </span>
              </div>
              {preflightJob.failure_message && (
                <p className="text-sm text-[#F87171] pl-6">{preflightJob.failure_message}</p>
              )}
              {preflightJob.phase === "completed" && !preflightJob.failure_message && (
                <p className="text-xs text-[#8B92A8] pl-6">
                  Passed at {fmtDate(preflightJob.updated_at)} — valid for {Math.round(PREFLIGHT_FRESHNESS_MS / 60000)} minutes.
                  {!preflightIsFresh && " This result has expired; run preflight again before installing."}
                </p>
              )}
              <p className="text-xs text-[#555D73] pl-6">
                The update service reports preflight as a single pass/fail result, not an itemized list of named
                checks — this UI reflects exactly what the backend returns.
              </p>
            </div>
          )}
        </div>
      )}

      {/* Downtime warning + Install */}
      {selectedVersion && (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-5">
          <div className="flex items-start gap-2 mb-4 bg-[#FBBF24]/5 border border-[#FBBF24]/30 rounded-lg p-3">
            <AlertTriangle className="w-4 h-4 text-[#FBBF24] mt-0.5 shrink-0" />
            <p className="text-sm text-[#E8EAF0]">
              Installing will stop and restart the Orvix service. Mail delivery and the admin console will be
              briefly unavailable during the update.
            </p>
          </div>
          <button
            onClick={() => setReauthAction("install")}
            disabled={!canInstall}
            className="flex items-center gap-2 bg-[#4F7CFF] text-white rounded px-4 py-2 text-sm hover:bg-[#3D6AE8] disabled:opacity-50"
          >
            <Download className="w-4 h-4" /> Install Update
          </button>
          {!preflightIsFresh && (
            <p className="text-xs text-[#8B92A8] mt-2">
              Run a passing, unexpired preflight for v{selectedVersion} to enable Install.
            </p>
          )}
        </div>
      )}

      {/* Rollback snapshots */}
      <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-5">
        <h3 className="text-[#E8EAF0] font-medium mb-3 flex items-center gap-2">
          <RotateCcw className="w-4 h-4 text-[#4F7CFF]" /> Rollback Snapshots
        </h3>
        {snapshotsQuery.isLoading ? (
          <p className="text-[#8B92A8] text-sm">Loading snapshots...</p>
        ) : snapshots.length === 0 ? (
          <p className="text-[#8B92A8] text-sm">No rollback snapshots available.</p>
        ) : (
          <div className="space-y-2">
            {snapshots.map((s) => (
              <div key={s.id} className="flex items-center justify-between p-3 bg-[#0C0E12] rounded-lg">
                <div>
                  <p className="text-sm text-[#E8EAF0] font-mono">v{s.source_version} ({s.source_commit.slice(0, 8)})</p>
                  <p className="text-xs text-[#8B92A8]">
                    {fmtDate(s.created_at)}
                    {s.last_known_good ? " · last known good" : ""}
                    {!s.verified ? " · unverified" : ""}
                  </p>
                </div>
                <button
                  onClick={() => { setRollbackTarget(s.id); setReauthAction("rollback"); }}
                  disabled={!s.verified || jobRunning}
                  className="flex items-center gap-2 bg-[#F87171]/10 text-[#F87171] rounded px-3 py-1.5 text-sm hover:bg-[#F87171]/20 disabled:opacity-50"
                >
                  <RotateCcw className="w-4 h-4" /> Roll Back
                </button>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* History */}
      <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl overflow-hidden">
        <div className="p-5 pb-0">
          <h3 className="text-[#E8EAF0] font-medium mb-3 flex items-center gap-2">
            <HistoryIcon className="w-4 h-4 text-[#4F7CFF]" /> History
          </h3>
        </div>
        {historyQuery.isLoading ? (
          <p className="text-[#8B92A8] text-sm p-5 pt-0">Loading history...</p>
        ) : history.length === 0 ? (
          <p className="text-[#8B92A8] text-sm p-5 pt-0">No update history yet.</p>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[#2A2F3E]">
                <th className="text-left p-3 text-[#8B92A8] font-medium">Job</th>
                <th className="text-left p-3 text-[#8B92A8] font-medium">Kind</th>
                <th className="text-left p-3 text-[#8B92A8] font-medium">Version</th>
                <th className="text-left p-3 text-[#8B92A8] font-medium">Result</th>
                <th className="text-left p-3 text-[#8B92A8] font-medium">Updated</th>
              </tr>
            </thead>
            <tbody>
              {history.map((j) => (
                <tr key={j.id} className="border-b border-[#2A2F3E] hover:bg-[#1A1E26]">
                  <td className="p-3 text-[#8B92A8] font-mono text-xs">{j.id}</td>
                  <td className="p-3 text-[#E8EAF0]">{j.kind}</td>
                  <td className="p-3 text-[#E8EAF0] font-mono">v{j.requested_version}</td>
                  <td className="p-3"><StatusBadge phase={j.phase} /></td>
                  <td className="p-3 text-[#8B92A8]">{fmtDate(j.updated_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {reauthAction === "install" && (
        <ReauthModal
          title="Confirm Install"
          pending={installMutation.isPending}
          error={installMutation.isError ? (installMutation.error as Error).message : null}
          onCancel={() => setReauthAction(null)}
          onConfirm={(password) => installMutation.mutate(password)}
        />
      )}
      {reauthAction === "rollback" && (
        <ReauthModal
          title="Confirm Rollback"
          pending={rollbackMutation.isPending}
          error={rollbackMutation.isError ? (rollbackMutation.error as Error).message : null}
          onCancel={() => setReauthAction(null)}
          onConfirm={(password) => rollbackMutation.mutate(password)}
        />
      )}
    </div>
  );
}
