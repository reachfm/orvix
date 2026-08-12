import { useState } from "react";
import { Loader2, AlertCircle, RefreshCw } from "lucide-react";
import TenantScopeBanner from "../tenant-context/components/TenantScopeBanner";
import { useTenantScope } from "../tenant-context/queries";
import { useDeliverabilityEvents, useDeliverabilityMetrics } from "./queries";
import PaginationControls from "../components/PaginationControls";
import { formatNumber, formatPercent, formatTimestamp, realPercent, signalTypeLabel } from "./formatters";
import type { SignalType } from "./contract";
import { safeErrorInfo } from "../errors";

const EVENT_PAGE_SIZE = 100;

function defaultWindow(): { start: string; end: string } {
  const end = new Date();
  const start = new Date(end.getTime() - 24 * 60 * 60 * 1000);
  return { start: start.toISOString(), end: end.toISOString() };
}

function MetricCard({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4">
      <p className="text-xs font-medium text-[var(--text-secondary)]">{label}</p>
      <p className="text-xl font-semibold text-[var(--text-primary)] mt-1">{value}</p>
      {sub && <p className="text-xs text-[var(--text-muted)] mt-0.5">{sub}</p>}
    </div>
  );
}

function BreakdownTable({ title, rows }: { title: string; rows: ReadonlyArray<{ Key: string; Count: number }> }) {
  if (!rows || rows.length === 0) {
    return (
      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4">
        <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-1">{title}</h3>
        <p className="text-xs text-[var(--text-muted)]">No data for this window.</p>
      </div>
    );
  }
  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4">
      <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-2">{title}</h3>
      <table className="w-full text-sm" aria-label={title}>
        <tbody>
          {rows.map((r) => (
            <tr key={r.Key} className="border-b border-[var(--bg-subtle)] last:border-0">
              <td className="py-1.5 text-[var(--text-primary)]">{r.Key}</td>
              <td className="py-1.5 text-right text-[var(--text-secondary)]">{formatNumber(r.Count)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/**
 * Deliverability — real metrics and events only. Rates come from the
 * returned numerators/denominators; time-series buckets render as a
 * table when present (the backend returns hourly/daily buckets). No
 * synthetic charts, reputation scores, or trend lines are fabricated.
 */
export default function DeliverabilityPage() {
  const { data: scope } = useTenantScope();
  const tenantId = scope?.tenantId ?? null;
  const [windowStart, setWindowStart] = useState(() => defaultWindow().start);
  const [windowEnd, setWindowEnd] = useState(() => defaultWindow().end);
  const [domainFilter, setDomainFilter] = useState("");
  const [typeFilter, setTypeFilter] = useState("");
  const [page, setPage] = useState(0);
  const [eventError, setEventError] = useState<unknown>(null);

  const metricsQ = useDeliverabilityMetrics(tenantId, windowStart, windowEnd);
  const eventsQ = useDeliverabilityEvents(tenantId, {
    domain: domainFilter || undefined,
    type: (typeFilter || undefined) as SignalType | undefined,
    limit: EVENT_PAGE_SIZE,
    offset: page * EVENT_PAGE_SIZE,
  });

  const summary = metricsQ.data?.summary;
  const events = eventsQ.data?.events ?? [];
  const total = eventsQ.data?.total ?? 0;

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Deliverability</h2>
        <p className="text-sm text-[var(--text-secondary)]">
          Real delivery evidence per tenant: aggregated metrics and safe, paginated events. Percentages are computed only
          from returned totals.
        </p>
      </div>

      <TenantScopeBanner />

      {tenantId === null ? (
        <div className="border border-[var(--border)] rounded-lg p-6 bg-[var(--bg-surface)]">
          <p className="text-sm font-medium text-[var(--text-primary)]">Select a tenant</p>
          <p className="text-sm text-[var(--text-secondary)] mt-1">
            Platform deliverability routes require an explicit target tenant id in the path.
          </p>
        </div>
      ) : (
        <>
          <div className="flex flex-wrap items-end gap-3">
            <label className="text-sm text-[var(--text-secondary)]">
              From
              <input
                type="datetime-local"
                value={toLocalInput(windowStart)}
                onChange={(e) => setWindowStart(fromLocalInput(e.target.value))}
                className="ml-2 px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
              />
            </label>
            <label className="text-sm text-[var(--text-secondary)]">
              To
              <input
                type="datetime-local"
                value={toLocalInput(windowEnd)}
                onChange={(e) => setWindowEnd(fromLocalInput(e.target.value))}
                className="ml-2 px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
              />
            </label>
            <button
              type="button"
              onClick={() => { const w = defaultWindow(); setWindowStart(w.start); setWindowEnd(w.end); }}
              className="inline-flex items-center gap-2 px-3 py-2 text-sm rounded border border-[var(--border)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
            >
              <RefreshCw size={14} /> Last 24h
            </button>
          </div>

          {metricsQ.isLoading ? (
            <div className="flex items-center justify-center h-48">
              <Loader2 size={24} className="text-[var(--accent)] animate-spin" />
            </div>
          ) : metricsQ.error ? (
            <div className="bg-[var(--bg-surface)] border border-[var(--danger)]/30 rounded-xl p-6 flex items-start gap-3" role="alert">
              <AlertCircle size={20} className="text-[var(--danger)] shrink-0" />
              <div>
                <p className="text-[var(--danger)] text-sm font-medium">{safeErrorInfo(metricsQ.error).title}</p>
                <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(metricsQ.error).detail}</p>
              </div>
            </div>
          ) : summary ? (
            <>
              <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                <MetricCard label="Volume" value={formatNumber(summary.volume)} />
                <MetricCard label="Delivered" value={formatNumber(summary.delivered)} sub={formatPercent(realPercent(summary.delivered, summary.volume))} />
                <MetricCard label="Failed" value={formatNumber(summary.failed)} sub={formatPercent(realPercent(summary.failed, summary.volume))} />
                <MetricCard label="Deferred" value={formatNumber(summary.deferred)} sub={formatPercent(realPercent(summary.deferred, summary.volume))} />
                <MetricCard label="Bounced" value={formatNumber(summary.bounced)} sub={formatPercent(realPercent(summary.bounced, summary.volume))} />
                <MetricCard label="Policy denied" value={formatNumber(summary.policy_denied)} sub={formatPercent(realPercent(summary.policy_denied, summary.volume))} />
                <MetricCard label="Suppressed" value={formatNumber(summary.suppressed)} sub={formatPercent(realPercent(summary.suppressed, summary.volume))} />
                <MetricCard label="Complaints" value={formatNumber(summary.complaints)} sub={formatPercent(realPercent(summary.complaints, summary.volume))} />
              </div>

              <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <BreakdownTable title="By category" rows={summary.by_category} />
                <BreakdownTable title="By domain" rows={summary.by_domain} />
                <BreakdownTable title="By relay/provider" rows={summary.by_provider} />
              </div>

              {summary.time_buckets.length > 0 ? (
                <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4">
                  <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-2">
                    Time series ({summary.bucket_size} buckets)
                  </h3>
                  <div className="overflow-x-auto max-h-72 overflow-y-auto">
                    <table className="w-full text-sm" aria-label="Deliverability time series">
                      <thead>
                        <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                          <th className="py-1.5 pr-3">Bucket</th>
                          <th className="py-1.5 pr-3 text-right">Delivered</th>
                          <th className="py-1.5 pr-3 text-right">Failed</th>
                          <th className="py-1.5 pr-3 text-right">Other</th>
                          <th className="py-1.5 text-right">Total</th>
                        </tr>
                      </thead>
                      <tbody>
                        {summary.time_buckets.map((b) => (
                          <tr key={b.start} className="border-b border-[var(--bg-subtle)] last:border-0">
                            <td className="py-1.5 pr-3 text-[var(--text-primary)]">{formatTimestamp(b.start)}</td>
                            <td className="py-1.5 pr-3 text-right text-[var(--text-secondary)]">{formatNumber(b.delivered)}</td>
                            <td className="py-1.5 pr-3 text-right text-[var(--text-secondary)]">{formatNumber(b.failed)}</td>
                            <td className="py-1.5 pr-3 text-right text-[var(--text-secondary)]">{formatNumber(b.other)}</td>
                            <td className="py-1.5 text-right text-[var(--text-secondary)]">{formatNumber(b.total)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </div>
              ) : (
                <p className="text-xs text-[var(--text-muted)]">
                  No time-series buckets were returned for this window.
                </p>
              )}
            </>
          ) : null}

          {/* Events */}
          <div className="flex flex-wrap items-center gap-2">
            <input
              value={domainFilter}
              onChange={(e) => { setDomainFilter(e.target.value); setPage(0); }}
              placeholder="Filter by domain…"
              aria-label="Filter events by domain"
              className="flex-1 max-w-xs px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
            />
            <input
              value={typeFilter}
              onChange={(e) => { setTypeFilter(e.target.value); setPage(0); }}
              placeholder="Filter by type…"
              aria-label="Filter events by type"
              className="flex-1 max-w-xs px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
            />
          </div>

          {eventsQ.isLoading ? (
            <div className="flex items-center justify-center h-32">
              <Loader2 size={24} className="text-[var(--accent)] animate-spin" />
            </div>
          ) : eventsQ.error ? (
            <div className="bg-[var(--bg-surface)] border border-[var(--danger)]/30 rounded-xl p-6 flex items-start gap-3" role="alert">
              <AlertCircle size={20} className="text-[var(--danger)] shrink-0" />
              <div>
                <p className="text-[var(--danger)] text-sm font-medium">{safeErrorInfo(eventsQ.error).title}</p>
                <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(eventsQ.error).detail}</p>
              </div>
            </div>
          ) : events.length === 0 ? (
            <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-8 text-center text-[var(--text-secondary)] text-sm">
              No delivery events for the current filters and window.
            </div>
          ) : (
            <>
              <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
                <div className="overflow-x-auto">
                  <table className="w-full text-sm" aria-label="Deliverability events">
                    <thead>
                      <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                        <th className="p-3">Recorded</th>
                        <th className="p-3">Type</th>
                        <th className="p-3">Category</th>
                        <th className="p-3">Dimension</th>
                        <th className="p-3">Value</th>
                        <th className="p-3 text-right">Latency</th>
                      </tr>
                    </thead>
                    <tbody>
                      {events.map((ev) => (
                        <tr key={ev.id} className="border-b border-[var(--bg-subtle)]">
                          <td className="p-3 text-[var(--text-secondary)]">{formatTimestamp(ev.recorded_at)}</td>
                          <td className="p-3 text-[var(--text-primary)]">{signalTypeLabel(ev.type)}</td>
                          <td className="p-3 text-[var(--text-secondary)]">{ev.category}</td>
                          <td className="p-3 text-[var(--text-secondary)]">{ev.dimension}</td>
                          <td className="p-3 text-[var(--text-secondary)]">{ev.dimension_value}</td>
                          <td className="p-3 text-right text-[var(--text-secondary)]">{ev.latency_ms ? `${ev.latency_ms} ms` : "—"}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
              <PaginationControls page={page} pageSize={EVENT_PAGE_SIZE} total={total} onChange={setPage} />
            </>
          )}
        </>
      )}
    </div>
  );
}

function toLocalInput(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function fromLocalInput(v: string): string {
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return "";
  return d.toISOString();
}
