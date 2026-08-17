import { AlertCircle, Loader2 } from "lucide-react";
import { PieChart, Pie, Cell, ResponsiveContainer } from "recharts";
import { useMonitoringHealthQuery } from "../../monitoring/queries";
import { formatBytes } from "../../monitoring/format";

const USED_COLOR = "var(--accent-violet)";
const FREE_COLOR = "var(--bg-subtle)";

// StorageDonut renders a real Used/Free breakdown from
// GET /api/v1/monitoring/health's disk[] entries (totalBytes/
// usedBytes/freeBytes/usedPct — no synthetic values). Multiple
// mounts are NOT silently collapsed: the donut shows the aggregate
// (same aggregation InfraKPICards uses, for visual consistency), and
// every individual mount is listed underneath with its own real
// used/total/pct so nothing is hidden.
export default function StorageDonut() {
  const healthQ = useMonitoringHealthQuery();

  if (healthQ.isLoading) {
    return (
      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-2xl p-6 flex items-center justify-center h-64" role="status">
        <Loader2 size={20} className="animate-spin text-[var(--text-muted)]" />
      </div>
    );
  }
  if (healthQ.error) {
    return (
      <div className="bg-[var(--bg-surface)] border border-[var(--danger)]/30 rounded-2xl p-6 flex items-center gap-2 text-sm text-[var(--danger)]" role="alert">
        <AlertCircle size={16} /> Failed to load storage metrics.
      </div>
    );
  }
  const disks = healthQ.data?.disk ?? [];
  if (disks.length === 0) {
    return (
      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-2xl p-6">
        <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-1">Storage Usage</h3>
        <p className="text-sm text-[var(--text-muted)] mt-4">No disk metrics reported by this host.</p>
      </div>
    );
  }

  const totalBytes = disks.reduce((s, d) => s + d.totalBytes, 0);
  const usedBytes = disks.reduce((s, d) => s + d.usedBytes, 0);
  const freeBytes = Math.max(0, totalBytes - usedBytes);
  const usedPct = totalBytes > 0 ? Math.round((usedBytes / totalBytes) * 100) : 0;

  const chartData = totalBytes > 0
    ? [
        { name: "Used", value: usedBytes },
        { name: "Free", value: freeBytes },
      ]
    : [{ name: "No data", value: 1 }];

  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-2xl p-6">
      <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-4">Storage Usage</h3>
      <div className="flex flex-col sm:flex-row items-center gap-6">
        <div className="relative w-40 h-40 flex-shrink-0" role="img" aria-label={`Storage: ${formatBytes(usedBytes)} used of ${formatBytes(totalBytes)}, ${usedPct}% used`}>
          <ResponsiveContainer width="100%" height="100%">
            <PieChart>
              <Pie
                data={chartData}
                dataKey="value"
                innerRadius={54}
                outerRadius={72}
                startAngle={90}
                endAngle={-270}
                stroke="none"
              >
                {totalBytes > 0 ? (
                  <>
                    <Cell fill={USED_COLOR} />
                    <Cell fill={FREE_COLOR} />
                  </>
                ) : (
                  <Cell fill="var(--bg-subtle)" />
                )}
              </Pie>
            </PieChart>
          </ResponsiveContainer>
          <div className="absolute inset-0 flex flex-col items-center justify-center pointer-events-none">
            <span className="text-lg font-semibold text-[var(--text-primary)]">{formatBytes(usedBytes)}</span>
            <span className="text-[11px] text-[var(--text-muted)]">of {formatBytes(totalBytes)} used</span>
          </div>
        </div>

        <div className="flex-1 w-full space-y-3">
          <div className="grid grid-cols-2 gap-3 text-sm">
            <Metric label="Total" value={formatBytes(totalBytes)} />
            <Metric label="Used" value={formatBytes(usedBytes)} dotColor={USED_COLOR} />
            <Metric label="Free" value={formatBytes(freeBytes)} />
            <Metric label="Used %" value={`${usedPct}%`} />
          </div>
          {disks.length > 1 && (
            <div className="pt-2 border-t border-[var(--border)] space-y-1.5">
              {disks.map((d) => (
                <div key={d.label} className="flex items-center justify-between text-xs">
                  <span className="text-[var(--text-secondary)] truncate">{d.label}</span>
                  <span className="text-[var(--text-muted)] flex-shrink-0 ml-2">
                    {formatBytes(d.usedBytes)} / {formatBytes(d.totalBytes)} ({d.usedPct}%)
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function Metric({ label, value, dotColor }: { label: string; value: string; dotColor?: string }) {
  return (
    <div>
      <div className="flex items-center gap-1.5 text-[11px] text-[var(--text-muted)] mb-0.5">
        {dotColor && <span className="w-2 h-2 rounded-full flex-shrink-0" style={{ backgroundColor: dotColor }} />}
        {label}
      </div>
      <p className="text-sm font-medium text-[var(--text-primary)]">{value}</p>
    </div>
  );
}
