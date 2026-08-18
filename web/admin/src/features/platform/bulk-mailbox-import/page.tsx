import { useRef, useState } from "react";
import { Loader2, AlertCircle, Download, Upload } from "lucide-react";
import TenantScopeBanner from "../tenant-context/components/TenantScopeBanner";
import { useTenantScope } from "../tenant-context/queries";
import { usePlatformDomains } from "../domains/queries";
import { useBulkMailboxJobs } from "./queries";
import { useStageBulkMailboxUpload, useValidateBulkMailboxUpload, useCreateBulkMailboxJob } from "./mutations";
import { downloadBulkMailboxTemplate } from "./api";
import { safeErrorInfo } from "../errors";
import PaginationControls from "../components/PaginationControls";
import JobDetailDrawer from "./components/JobDetailDrawer";
import {
  BULK_CONFLICT_POLICY_OPTIONS,
  BULK_STRATEGY_OPTIONS,
  type BulkConflictPolicy,
  type BulkStageResult,
  type BulkStrategy,
  type BulkValidationResult,
} from "./contract";

const JOBS_PAGE_SIZE = 25;

const ALLOWED_EXTENSIONS = [".csv", ".xlsx"];

/**
 * Platform Bulk Mailbox Provisioning (CSV/XLSX import) for one EXPLICIT
 * tenant. Distinct from features/platform/bulk-mailboxes (bulk status
 * change on existing mailboxes) — this feature durably creates new
 * mailboxes from an uploaded sheet via stage -> validate -> create job
 * -> execute -> poll.
 */
export default function BulkMailboxImportPage() {
  const { data: scope } = useTenantScope();
  const tenantId = scope?.tenantId ?? null;

  const [domainId, setDomainId] = useState<number | "">("");
  const [selectedJobId, setSelectedJobId] = useState<number | null>(null);
  const [jobsPage, setJobsPage] = useState(0);

  const [templateError, setTemplateError] = useState<unknown>(null);
  const [templateDownloading, setTemplateDownloading] = useState<"csv" | "xlsx" | null>(null);

  const [staged, setStaged] = useState<BulkStageResult | null>(null);
  const [validation, setValidation] = useState<BulkValidationResult | null>(null);
  const [strategy, setStrategy] = useState<BulkStrategy>("partial");
  const [conflictPolicy, setConflictPolicy] = useState<BulkConflictPolicy>("fail");
  const [stageIdemKey, setStageIdemKey] = useState<string | null>(null);
  const [createIdemKey, setCreateIdemKey] = useState<string | null>(null);
  const [workflowError, setWorkflowError] = useState<unknown>(null);
  const [fileError, setFileError] = useState<string | null>(null);
  const [createdJobId, setCreatedJobId] = useState<number | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const domainsQ = usePlatformDomains(tenantId, { limit: 200, offset: 0 });
  const jobsQ = useBulkMailboxJobs(tenantId, jobsPage, JOBS_PAGE_SIZE);
  const stageMut = useStageBulkMailboxUpload(tenantId);
  const validateMut = useValidateBulkMailboxUpload(tenantId);
  const createJobMut = useCreateBulkMailboxJob(tenantId);

  const domains = domainsQ.data?.domains ?? [];
  const jobs = jobsQ.data?.jobs ?? [];
  const jobsTotal = jobsQ.data?.total ?? 0;

  const resetWorkflow = () => {
    setStaged(null);
    setValidation(null);
    setStageIdemKey(null);
    setCreateIdemKey(null);
    setWorkflowError(null);
    setFileError(null);
    setCreatedJobId(null);
    if (fileInputRef.current) fileInputRef.current.value = "";
  };

  const downloadTemplate = async (format: "csv" | "xlsx") => {
    setTemplateError(null);
    setTemplateDownloading(format);
    try {
      const blob = await downloadBulkMailboxTemplate(format);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `bulk-mailbox-import-template.${format}`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } catch (e) {
      setTemplateError(e);
    } finally {
      setTemplateDownloading(null);
    }
  };

  const onFileChosen = (file: File | null) => {
    resetWorkflow();
    if (!file || tenantId === null) return;
    const lower = file.name.toLowerCase();
    if (!ALLOWED_EXTENSIONS.some((ext) => lower.endsWith(ext))) {
      setFileError("Only .csv or .xlsx files are accepted.");
      return;
    }
    const key = crypto.randomUUID();
    setStageIdemKey(key);
    stageMut.mutate(
      { file, idempotencyKey: key },
      {
        onSuccess: (res) => setStaged(res),
        onError: (e) => setWorkflowError(e),
      },
    );
  };

  const runValidate = () => {
    if (!staged || domainId === "" || tenantId === null) return;
    setWorkflowError(null);
    validateMut.mutate(
      { staging_id: staged.staging_id, source_hash: staged.source_hash, format: staged.format, domain_id: domainId },
      {
        onSuccess: (res) => setValidation(res),
        onError: (e) => setWorkflowError(e),
      },
    );
  };

  const createJob = () => {
    if (!staged || !validation || domainId === "" || tenantId === null) return;
    const key = createIdemKey ?? crypto.randomUUID();
    setCreateIdemKey(key);
    setWorkflowError(null);
    createJobMut.mutate(
      {
        body: {
          staging_id: staged.staging_id,
          source_hash: staged.source_hash,
          format: staged.format,
          domain_id: domainId,
          strategy,
          conflict_policy: conflictPolicy,
        },
        idempotencyKey: key,
      },
      {
        onSuccess: (res) => {
          setCreatedJobId(res.job.id);
          setSelectedJobId(res.job.id);
        },
        onError: (e) => setWorkflowError(e),
      },
    );
  };

  const hasBlockingErrors = validation ? validation.invalid_rows > 0 && validation.valid_rows === 0 : false;

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Bulk Mailbox Provisioning</h2>
        <p className="text-sm text-[var(--text-secondary)]">
          Durable CSV/XLSX mailbox import for one explicit tenant. Bulk-created mailboxes use the standard
          activation / forgot-password flow — no password file is ever produced by this workflow.
        </p>
      </div>

      <TenantScopeBanner />

      <div className="border border-[var(--border)] rounded-xl p-4 bg-[var(--bg-surface)] space-y-2">
        <p className="text-sm font-medium text-[var(--text-primary)]">1. Download a template</p>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={() => downloadTemplate("csv")}
            disabled={templateDownloading !== null}
            className="inline-flex items-center gap-1.5 px-3 py-2 text-sm rounded border border-[var(--border)] text-[var(--text-primary)] disabled:opacity-40"
          >
            {templateDownloading === "csv" ? <Loader2 size={14} className="animate-spin" /> : <Download size={14} />}
            CSV template
          </button>
          <button
            type="button"
            onClick={() => downloadTemplate("xlsx")}
            disabled={templateDownloading !== null}
            className="inline-flex items-center gap-1.5 px-3 py-2 text-sm rounded border border-[var(--border)] text-[var(--text-primary)] disabled:opacity-40"
          >
            {templateDownloading === "xlsx" ? <Loader2 size={14} className="animate-spin" /> : <Download size={14} />}
            XLSX template
          </button>
        </div>
        {templateError !== null && (
          <p className="text-sm text-[var(--danger)]" role="alert">{safeErrorInfo(templateError).detail}</p>
        )}
      </div>

      {tenantId === null ? (
        <div className="border border-[var(--border)] rounded-lg p-6 bg-[var(--bg-surface)]">
          <p className="text-sm font-medium text-[var(--text-primary)]">Select a tenant</p>
          <p className="text-sm text-[var(--text-secondary)] mt-1">
            Bulk mailbox provisioning requires an explicit target tenant. No tenant is assumed or derived.
          </p>
        </div>
      ) : (
        <div className="border border-[var(--border)] rounded-xl p-4 bg-[var(--bg-surface)] space-y-4">
          <p className="text-sm font-medium text-[var(--text-primary)]">2. Upload a sheet for this tenant's domain</p>

          <label className="block text-sm">
            <span className="text-[var(--text-secondary)]">Domain</span>
            <select
              value={domainId}
              onChange={(e) => { setDomainId(e.target.value === "" ? "" : Number(e.target.value)); resetWorkflow(); }}
              className="mt-1 w-full max-w-sm px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
            >
              <option value="">Select a domain owned by this tenant…</option>
              {domains.map((d) => (
                <option key={d.id} value={d.id}>{d.name}</option>
              ))}
            </select>
          </label>

          <label className="flex items-center gap-2 text-sm">
            <span className="inline-flex items-center gap-1.5 px-3 py-2 rounded border border-[var(--border)] text-[var(--text-primary)] cursor-pointer">
              <Upload size={14} /> Choose .csv or .xlsx file
              <input
                ref={fileInputRef}
                type="file"
                accept=".csv,.xlsx"
                disabled={domainId === ""}
                onChange={(e) => onFileChosen(e.target.files?.[0] ?? null)}
                className="sr-only"
              />
            </span>
            {stageMut.isPending && <Loader2 size={14} className="animate-spin text-[var(--accent)]" />}
          </label>

          {fileError !== null && (
            <div className="border border-[var(--danger)]/30 rounded-lg p-3 text-sm flex items-start gap-2" role="alert">
              <AlertCircle size={16} className="text-[var(--danger)] shrink-0 mt-0.5" />
              <p className="text-[var(--danger)] font-medium">{fileError}</p>
            </div>
          )}

          {workflowError !== null && (
            <div className="border border-[var(--danger)]/30 rounded-lg p-3 text-sm flex items-start gap-2" role="alert">
              <AlertCircle size={16} className="text-[var(--danger)] shrink-0 mt-0.5" />
              <div>
                <p className="text-[var(--danger)] font-medium">{safeErrorInfo(workflowError).title}</p>
                <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(workflowError).detail}</p>
              </div>
            </div>
          )}

          {staged && !createdJobId && (
            <div className="border border-[var(--border)] rounded-lg p-3 text-sm space-y-2">
              <p className="text-[var(--text-primary)]">
                Staged {staged.row_count} row{staged.row_count === 1 ? "" : "s"} ({staged.format.toUpperCase()}).
              </p>
              {!validation ? (
                <button
                  type="button"
                  disabled={validateMut.isPending}
                  onClick={runValidate}
                  className="inline-flex items-center gap-2 px-3 py-2 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
                >
                  {validateMut.isPending && <Loader2 size={14} className="animate-spin" />}
                  Validate
                </button>
              ) : (
                <>
                  <p className="text-[var(--text-primary)]">
                    {validation.valid_rows} valid, {validation.invalid_rows} invalid of {validation.total_rows} total rows.
                    {validation.capacity_remaining >= 0 && ` Capacity remaining (advisory): ${validation.capacity_remaining}.`}
                  </p>

                  {validation.rows.filter((r) => r.status === "invalid").length > 0 && (
                    <div className="overflow-x-auto border border-[var(--border)] rounded max-h-56 overflow-y-auto">
                      <table className="w-full text-xs">
                        <thead>
                          <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                            <th className="p-1.5">Row</th>
                            <th className="p-1.5">Email</th>
                            <th className="p-1.5">Error</th>
                          </tr>
                        </thead>
                        <tbody>
                          {validation.rows.filter((r) => r.status === "invalid").map((r) => (
                            <tr key={r.row_number} className="border-b border-[var(--border)] last:border-0">
                              <td className="p-1.5 text-[var(--text-secondary)]">{r.row_number}</td>
                              <td className="p-1.5 text-[var(--text-primary)]">{r.email}</td>
                              <td className="p-1.5 text-[var(--text-secondary)]">{r.error_detail || r.error_code}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  )}

                  {hasBlockingErrors ? (
                    <p className="text-sm text-[var(--danger)]" role="alert">
                      No valid rows — fix the sheet and upload again before a job can be created.
                    </p>
                  ) : (
                    <div className="grid sm:grid-cols-2 gap-3">
                      <fieldset>
                        <legend className="text-xs text-[var(--text-secondary)] mb-1">Strategy</legend>
                        {BULK_STRATEGY_OPTIONS.map((o) => (
                          <label key={o.value} className="flex items-start gap-2 text-xs mb-1">
                            <input type="radio" name="strategy" checked={strategy === o.value} onChange={() => setStrategy(o.value)} className="mt-0.5" />
                            <span>{o.label}</span>
                          </label>
                        ))}
                      </fieldset>
                      <fieldset>
                        <legend className="text-xs text-[var(--text-secondary)] mb-1">Conflict policy</legend>
                        {BULK_CONFLICT_POLICY_OPTIONS.map((o) => (
                          <label key={o.value} className="flex items-start gap-2 text-xs mb-1">
                            <input type="radio" name="conflict_policy" checked={conflictPolicy === o.value} onChange={() => setConflictPolicy(o.value)} className="mt-0.5" />
                            <span>{o.label}</span>
                          </label>
                        ))}
                      </fieldset>
                      <div className="sm:col-span-2">
                        <button
                          type="button"
                          disabled={createJobMut.isPending}
                          onClick={createJob}
                          className="inline-flex items-center gap-2 px-3 py-2 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
                        >
                          {createJobMut.isPending && <Loader2 size={14} className="animate-spin" />}
                          Create job
                        </button>
                      </div>
                    </div>
                  )}
                </>
              )}
            </div>
          )}

          {createdJobId !== null && (
            <p className="text-sm text-[var(--success)]">
              Job #{createdJobId} created. Open it below to execute and monitor progress.
            </p>
          )}
        </div>
      )}

      {tenantId !== null && (
        <div>
          <h3 className="text-sm font-medium text-[var(--text-primary)] mb-2">Import jobs</h3>
          {jobsQ.isLoading ? (
            <Loader2 className="animate-spin text-[var(--accent)]" size={20} />
          ) : jobsQ.error ? (
            <p className="text-sm text-[var(--danger)]" role="alert">{safeErrorInfo(jobsQ.error).detail}</p>
          ) : jobs.length === 0 ? (
            <p className="text-sm text-[var(--text-secondary)]">No bulk import jobs for this tenant yet.</p>
          ) : (
            <>
              <div className="overflow-x-auto border border-[var(--border)] rounded-lg">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                      <th className="p-2">Job</th>
                      <th className="p-2">Status</th>
                      <th className="p-2">Rows</th>
                      <th className="p-2">Created</th>
                      <th className="p-2">Failed</th>
                      <th className="p-2"></th>
                    </tr>
                  </thead>
                  <tbody>
                    {jobs.map((j) => (
                      <tr key={j.id} className="border-b border-[var(--border)] last:border-0">
                        <td className="p-2 text-[var(--text-primary)]">#{j.id}</td>
                        <td className="p-2 text-[var(--text-primary)]">{j.status}</td>
                        <td className="p-2 text-[var(--text-secondary)]">{j.total_rows}</td>
                        <td className="p-2 text-[var(--text-secondary)]">{j.created_count}</td>
                        <td className="p-2 text-[var(--text-secondary)]">{j.failed_count}</td>
                        <td className="p-2">
                          <button type="button" onClick={() => setSelectedJobId(j.id)} className="text-[var(--accent)] hover:underline">
                            View
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <PaginationControls page={jobsPage} pageSize={JOBS_PAGE_SIZE} total={jobsTotal} onChange={setJobsPage} />
            </>
          )}
        </div>
      )}

      {tenantId !== null && selectedJobId !== null && (
        <JobDetailDrawer tenantId={tenantId} jobId={selectedJobId} onClose={() => setSelectedJobId(null)} />
      )}
    </div>
  );
}
