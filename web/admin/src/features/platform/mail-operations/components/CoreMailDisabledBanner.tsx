import { MailWarning } from "lucide-react";

export default function CoreMailDisabledBanner() {
  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-8 text-center">
      <MailWarning size={32} className="text-[var(--text-secondary)] mx-auto mb-3" />
      <p className="text-[var(--text-primary)] text-sm font-medium mb-1">CoreMail is disabled</p>
      <p className="text-[var(--text-secondary)] text-sm">The mail queue is not available on this deployment.</p>
    </div>
  );
}
