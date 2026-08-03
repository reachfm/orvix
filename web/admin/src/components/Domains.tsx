import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, useEffect, useRef, useCallback } from "react";
import { CheckCircle, XCircle, AlertTriangle, Plus, Trash2, RefreshCw, Copy, X, Shield, Loader2, Globe } from "lucide-react";
import { api, domainErrorMessage } from "../api";

interface EnterpriseDomain {
  id: number;
  name: string;
  status: string;
  plan?: string;
  mailbox_count?: number;
  dkim_enabled?: boolean;
  dkim_selector?: string;
}

interface ConfirmDelete {
  domain: EnterpriseDomain;
}

interface ConfirmRotate {
  domain: EnterpriseDomain;
}

interface DNSHealthCheck {
  selector?: string;
  status: string;
  expected?: string;
  observed?: string;
  reason?: string;
  checked_at: string;
  record_name?: string;
  configured?: boolean;
  public_txt?: string;
  matches_dns?: boolean;
}

interface DomainDNSHealth {
  domain_id: number;
  domain_name: string;
  operational_status: string;
  dns_health: string;
  health_score: number;
  last_checked_at?: string;
  mx: DNSHealthCheck | null;
  spf: DNSHealthCheck | null;
  dkim: DNSHealthCheck | null;
  dmarc: DNSHealthCheck | null;
  mtasts: DNSHealthCheck | null;
  tlsrpt: DNSHealthCheck | null;
}

interface DKIMResult {
  selector: string;
  public_dns_txt: string;
  dns_record_name: string;
}

function HealthBadge({ status }: { status: string }) {
  const color = status === "pass" ? "#34D399"
    : status === "warning" ? "#FBBF24"
    : status === "fail" ? "#F87171"
    : "#8B92A8";
  return (
    <span className="inline-flex items-center gap-1.5 text-xs">
      <span className="w-2 h-2 rounded-full shrink-0" style={{ backgroundColor: color }} />
      <span className="capitalize" style={{ color }}>{status || "unknown"}</span>
    </span>
  );
}

function StatusBadge({ status }: { status: string }) {
  const isActive = status === "active";
  const isSuspended = status === "suspended";
  return (
    <span className={`inline-flex items-center gap-1 px-2 py-1 text-xs rounded-full ${
      isActive ? "bg-[#34D399]/10 text-[#34D399]" :
      isSuspended ? "bg-[#F87171]/10 text-[#F87171]" :
      "bg-[#FBBF24]/10 text-[#FBBF24]"
    }`}>
      {isActive ? <CheckCircle size={10} /> : isSuspended ? <XCircle size={10} /> : <AlertTriangle size={10} />}
      {status || "unknown"}
    </span>
  );
}

function errorMessage(err: unknown, fallback: string): string {
  if (err && typeof err === "object" && "code" in (err as any) && typeof (err as any).code === "string") {
    return domainErrorMessage((err as any).code, (err as any).message || fallback);
  }
  return (err as Error)?.message || fallback;
}

function DNSRecordRow({ label, check, recordName }: { label: string; check: DNSHealthCheck | null; recordName?: string }) {
  if (!check) {
    return (
      <div className="py-3 border-b border-[#222736]">
        <p className="text-[#E8EAF0] text-sm font-medium mb-1">{label}</p>
        <p className="text-[#8B92A8] text-xs">Not checked</p>
      </div>
    );
  }

  return (
    <div className="py-3 border-b border-[#222736] space-y-1.5">
      <div className="flex items-center justify-between">
        <span className="text-[#E8EAF0] text-sm font-medium">{label}</span>
        <HealthBadge status={check.status} />
      </div>
      {recordName && (
        <p className="text-[#8B92A8] text-xs break-all select-all">
          <span className="text-[#555D73]">Record: </span>
          {recordName}
        </p>
      )}
      {check.expected !== undefined && check.expected !== null && (
        <p className="text-[#8B92A8] text-xs break-all select-all">
          <span className="text-[#555D73]">Expected: </span>
          {check.expected}
        </p>
      )}
      {check.observed !== undefined && check.observed !== null && (
        <p className="text-[#8B92A8] text-xs break-all select-all">
          <span className="text-[#555D73]">Observed: </span>
          {check.observed}
        </p>
      )}
      {check.reason && (
        <p className="text-[#F87171] text-xs">{check.reason}</p>
      )}
      {check.checked_at && (
        <p className="text-[#555D73] text-xs">
          Checked: {new Date(check.checked_at).toLocaleString()}
        </p>
      )}
    </div>
  );
}

function DKIMInstructions({ result }: { result: DKIMResult }) {
  return (
    <div className="bg-[#0C0E12] border border-[#2A2F3E] rounded-lg p-3 mt-2 text-xs font-mono text-[#8B92A8] space-y-2">
      <p className="text-[#34D399] text-sm font-medium">DKIM key rotated successfully. Update the DNS TXT record below. DKIM verification will fail until the new record propagates.</p>
      <div className="flex items-start gap-1">
        <span className="text-[#4F7CFF] shrink-0">Name:</span>
        <span className="text-[#E8EAF0] break-all select-all">{result.dns_record_name}</span>
      </div>
      <div className="flex items-start gap-1">
        <span className="text-[#4F7CFF] shrink-0">Value:</span>
        <span className="text-[#E8EAF0] break-all select-all">{result.public_dns_txt}</span>
      </div>
      <div className="flex items-start gap-1">
        <span className="text-[#4F7CFF] shrink-0">Selector:</span>
        <span className="text-[#E8EAF0]">{result.selector}</span>
      </div>
    </div>
  );
}

function DNSHealthDrawer({ domain, onClose }: { domain: EnterpriseDomain; onClose: () => void }) {
  const queryClient = useQueryClient();
  const drawerRef = useRef<HTMLDivElement>(null);
  const autoCheckRan = useRef(false);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;
  const [copiedField, setCopiedField] = useState<string | null>(null);

  const dnsQuery = useQuery({
    queryKey: ["dns-health", domain.id],
    queryFn: () => api.getEnterpriseDomainDNS(domain.id),
  });

  const verifyMutation = useMutation({
    mutationFn: () => api.verifyEnterpriseDomainDNS(domain.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["dns-health", domain.id] });
    },
  });

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        onCloseRef.current();
      }
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, []);

  useEffect(() => {
    if (drawerRef.current) {
      const first = drawerRef.current.querySelector<HTMLElement>("button, [tabindex]");
      first?.focus();
    }
  }, []);

  useEffect(() => {
    if (autoCheckRan.current) return;
    if (dnsQuery.isFetching || verifyMutation.isPending) return;
    const d = dnsQuery.data as DomainDNSHealth | undefined;
    const needsCheck = !d || d.health_score === 0;
    const isStale = d?.last_checked_at
      ? (Date.now() - new Date(d.last_checked_at).getTime()) > 5 * 60 * 1000
      : true;
    if ((needsCheck || isStale) && !verifyMutation.isPending) {
      autoCheckRan.current = true;
      verifyMutation.mutate();
    }
  }, [dnsQuery.data, dnsQuery.isFetching, verifyMutation.isPending, verifyMutation.mutate]);

  const handleCopy = useCallback(async (text: string, field: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopiedField(field);
      setTimeout(() => setCopiedField(null), 2000);
    } catch {
      // clipboard write failed
    }
  }, []);

  const healthData = dnsQuery.data as DomainDNSHealth | undefined;
  const isVerifying = verifyMutation.isPending;
  const isLoading = dnsQuery.isFetching && !healthData;

  return (
    <>
      <div
        className="fixed inset-0 bg-black/40 z-40"
        onClick={onClose}
        aria-hidden="true"
      />
      <div
        ref={drawerRef}
        className="fixed right-0 top-0 h-full w-96 max-w-full bg-[#13161C] border-l border-[#2A2F3E] z-50 flex flex-col shadow-2xl"
        role="dialog"
        aria-modal="true"
        aria-labelledby="dns-health-title"
      >
        <div className="shrink-0 p-5 border-b border-[#2A2F3E]">
          <div className="flex items-center justify-between mb-3">
            <h3 id="dns-health-title" className="text-lg font-semibold text-[#E8EAF0] flex items-center gap-2">
              <Shield size={18} className="text-[#4F7CFF]" />
              DNS Health: <span className="font-mono text-sm">{domain.name}</span>
            </h3>
            <button
              onClick={onClose}
              className="text-[#8B92A8] hover:text-[#E8EAF0] p-1 rounded"
              aria-label="Close"
            >
              <X size={18} />
            </button>
          </div>

          {healthData && (
            <div className="flex items-center gap-3 mb-3">
              <span className="text-3xl font-bold text-[#E8EAF0]">{healthData.health_score}%</span>
              <HealthBadge status={healthData.dns_health} />
            </div>
          )}

          <button
            onClick={() => { if (!isVerifying) verifyMutation.mutate(); }}
            disabled={isVerifying}
            className="w-full py-2 px-4 bg-[#4F7CFF] text-white rounded-lg text-sm hover:bg-[#3B5FD9] disabled:opacity-50 inline-flex items-center justify-center gap-2"
          >
            {isVerifying && <Loader2 size={14} className="animate-spin" />}
            {isVerifying ? "Checking DNS..." : "Check DNS Now"}
          </button>

          {verifyMutation.isError && (
            <p className="text-[#F87171] text-xs mt-2" role="alert">
              {errorMessage(verifyMutation.error, "Verification failed.")}
            </p>
          )}
        </div>

        <div className="flex-1 overflow-y-auto p-5">
          {isLoading ? (
            <div className="flex flex-col items-center justify-center h-full gap-3">
              <Loader2 size={24} className="animate-spin text-[#4F7CFF]" />
              <p className="text-[#8B92A8] text-sm">Checking DNS...</p>
            </div>
          ) : dnsQuery.isError ? (
            <div className="flex flex-col items-center justify-center h-full gap-3 text-center">
              <XCircle size={24} className="text-[#F87171]" />
              <p className="text-[#F87171] text-sm">
                {errorMessage(dnsQuery.error, "Failed to load DNS data.")}
              </p>
              <button
                onClick={() => { if (!isVerifying) verifyMutation.mutate(); }}
                disabled={isVerifying}
                className="text-sm text-[#4F7CFF] hover:underline"
              >
                Retry
              </button>
            </div>
          ) : healthData ? (
            <div>
              {healthData.last_checked_at && (
                <p className="text-[#555D73] text-xs mb-4">
                  Last checked: {new Date(healthData.last_checked_at).toLocaleString()}
                </p>
              )}

              <DNSRecordRow label="MX" check={healthData.mx} />
              <DNSRecordRow label="SPF" check={healthData.spf} />

              <div className="py-3 border-b border-[#222736] space-y-1.5">
                <div className="flex items-center justify-between">
                  <span className="text-[#E8EAF0] text-sm font-medium">DKIM</span>
                  {healthData.dkim ? <HealthBadge status={healthData.dkim.status} /> : null}
                </div>
                {!healthData.dkim ? (
                  <p className="text-[#8B92A8] text-xs">Not checked</p>
                ) : (
                  <>
                    {healthData.dkim.selector && (
                      <p className="text-[#8B92A8] text-xs">
                        <span className="text-[#555D73]">Selector: </span>
                        <span className="font-mono">{healthData.dkim.selector}</span>
                      </p>
                    )}
                    {healthData.dkim.record_name && (
                      <div>
                        <div className="flex items-center justify-between">
                          <span className="text-[#555D73] text-xs">Record Name:</span>
                          <button
                            onClick={() => handleCopy(healthData.dkim!.record_name!, "dkim-name")}
                            className="text-[#4F7CFF] hover:text-[#3B5FD9] p-0.5"
                            aria-label="Copy DKIM record name"
                          >
                            {copiedField === "dkim-name" ? (
                              <span className="text-xs text-[#34D399]">Copied!</span>
                            ) : (
                              <Copy size={12} />
                            )}
                          </button>
                        </div>
                        <p className="text-[#8B92A8] text-xs break-all select-all mt-0.5">
                          {healthData.dkim.record_name}
                        </p>
                      </div>
                    )}
                    {healthData.dkim.public_txt && (
                      <div>
                        <div className="flex items-center justify-between">
                          <span className="text-[#555D73] text-xs">Public TXT:</span>
                          <button
                            onClick={() => handleCopy(healthData.dkim!.public_txt!, "dkim-txt")}
                            className="text-[#4F7CFF] hover:text-[#3B5FD9] p-0.5"
                            aria-label="Copy DKIM TXT record"
                          >
                            {copiedField === "dkim-txt" ? (
                              <span className="text-xs text-[#34D399]">Copied!</span>
                            ) : (
                              <Copy size={12} />
                            )}
                          </button>
                        </div>
                        <p className="text-[#8B92A8] text-xs break-all select-all mt-0.5">
                          {healthData.dkim.public_txt}
                        </p>
                      </div>
                    )}
                    {healthData.dkim.configured !== undefined && (
                      <p className="text-xs">
                        <span className="text-[#555D73]">Configured: </span>
                        <span className={healthData.dkim.configured ? "text-[#34D399]" : "text-[#F87171]"}>
                          {healthData.dkim.configured ? "Yes" : "No"}
                        </span>
                      </p>
                    )}
                    {healthData.dkim.matches_dns !== undefined && (
                      <p className="text-xs">
                        <span className="text-[#555D73]">Matches DNS: </span>
                        <span className={healthData.dkim.matches_dns ? "text-[#34D399]" : "text-[#F87171]"}>
                          {healthData.dkim.matches_dns ? "Yes" : "No"}
                        </span>
                      </p>
                    )}
                    {healthData.dkim.reason && (
                      <p className="text-[#F87171] text-xs">{healthData.dkim.reason}</p>
                    )}
                    {healthData.dkim.checked_at && (
                      <p className="text-[#555D73] text-xs">
                        Checked: {new Date(healthData.dkim.checked_at).toLocaleString()}
                      </p>
                    )}
                  </>
                )}
              </div>

              <DNSRecordRow label="DMARC" check={healthData.dmarc} />
              <DNSRecordRow label="MTA-STS" check={healthData.mtasts} />
              <DNSRecordRow label="TLS-RPT" check={healthData.tlsrpt} />
            </div>
          ) : (
            <div className="flex flex-col items-center justify-center h-full gap-3 text-center">
              <Globe size={24} className="text-[#8B92A8]" />
              <p className="text-[#8B92A8] text-sm">No DNS data available</p>
            </div>
          )}
        </div>
      </div>
    </>
  );
}

export default function Domains() {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [newDomain, setNewDomain] = useState("");
  const [confirmDelete, setConfirmDelete] = useState<ConfirmDelete | null>(null);
  const [confirmRotate, setConfirmRotate] = useState<ConfirmRotate | null>(null);
  const [dnsPanel, setDnsPanel] = useState<EnterpriseDomain | null>(null);
  const [dkimR, setDkimR] = useState<Record<number, DKIMResult | null>>({});
  const [dkimErr, setDkimErr] = useState<Record<number, string | null>>({});

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["enterprise-domains"],
    queryFn: () => api.listDomainsEnterprise(),
  });

  const createMutation = useMutation({
    mutationFn: (name: string) => api.createDomainEnterprise({ name }),
    onSuccess: () => {
      setShowCreate(false);
      setNewDomain("");
      queryClient.invalidateQueries({ queryKey: ["enterprise-domains"] });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => api.deleteDomainEnterprise(id),
    onSuccess: () => {
      setConfirmDelete(null);
      queryClient.invalidateQueries({ queryKey: ["enterprise-domains"] });
    },
  });

  const generateDKIM = useMutation({
    mutationFn: (d: EnterpriseDomain) => api.generateDomainDKIM(d.id, d.dkim_selector || "mail"),
    onSuccess: (_data, d) => {
      setDkimErr((p) => ({ ...p, [d.id]: null }));
      queryClient.invalidateQueries({ queryKey: ["enterprise-domains"] });
    },
    onError: (err, d) => {
      setDkimErr((p) => ({ ...p, [d.id]: errorMessage(err, "DKIM generation failed.") }));
    },
  });

  const rotateDKIM = useMutation({
    mutationFn: (d: EnterpriseDomain) => api.rotateDomainDKIM(d.id, d.dkim_selector || "mail"),
    onSuccess: (data, d) => {
      setConfirmRotate(null);
      setDkimErr((p) => ({ ...p, [d.id]: null }));
      const result = (data as any)?.dkim as DKIMResult | undefined;
      if (result) {
        setDkimR((p) => ({ ...p, [d.id]: result }));
      }
      queryClient.invalidateQueries({ queryKey: ["enterprise-domains"] });
    },
    onError: (err, d) => {
      setDkimErr((p) => ({ ...p, [d.id]: errorMessage(err, "DKIM rotation failed.") }));
    },
  });

  const dismissDkim = (id: number) => {
    setDkimR((p) => ({ ...p, [id]: null }));
    setDkimErr((p) => ({ ...p, [id]: null }));
  };

  if (isLoading) return <p className="text-[#8B92A8]">Loading...</p>;
  if (error) {
    return (
      <div className="flex flex-col gap-3">
        <p className="text-[#F87171]">Failed to load domains: {errorMessage(error, "Failed to load domains.")}</p>
        <button onClick={() => refetch()} className="text-sm text-[#4F7CFF] hover:underline text-left">Retry</button>
      </div>
    );
  }

  const raw = (data as any)?.domains ?? (Array.isArray(data) ? data : []);
  const items: EnterpriseDomain[] = Array.isArray(raw) ? raw : [];
  const filtered = items.filter(
    (d) => !search || (d.name || "").toLowerCase().includes(search.toLowerCase())
  );

  return (
    <div>
      {/* Delete confirmation */}
      {confirmDelete && (
        <div
          className="fixed inset-0 bg-black/60 flex items-center justify-center z-50"
          role="dialog"
          aria-modal="true"
          aria-labelledby="delete-domain-title"
        >
          <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-6 w-96 max-w-full">
            <h3 id="delete-domain-title" className="text-lg font-semibold text-[#E8EAF0] mb-2">
              Delete Domain
            </h3>
            <p className="text-sm text-[#8B92A8] mb-6">
              Permanently delete <span className="text-[#E8EAF0] font-mono">{confirmDelete.domain.name}</span>?{" "}
              {(confirmDelete.domain.mailbox_count ?? 0) > 0
                ? "This domain still has mailboxes — delete them first, then remove the domain."
                : "This cannot be undone."}
            </p>
            {deleteMutation.isError && (
              <p className="text-[#F87171] text-xs mb-3" role="alert">
                {errorMessage(deleteMutation.error, "Deletion failed.")}
              </p>
            )}
            <div className="flex gap-3 justify-end">
              <button onClick={() => setConfirmDelete(null)}
                className="px-4 py-2 text-sm text-[#8B92A8] hover:text-[#E8EAF0] rounded-lg border border-[#2A2F3E]">
                Cancel
              </button>
              <button
                onClick={() => { if (!deleteMutation.isPending) deleteMutation.mutate(confirmDelete.domain.id); }}
                disabled={deleteMutation.isPending}
                className="px-4 py-2 text-sm rounded-lg bg-[#F87171] text-white hover:bg-red-500 disabled:opacity-50">
                {deleteMutation.isPending ? "Deleting..." : "Delete Domain"}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Rotate confirmation */}
      {confirmRotate && (
        <div
          className="fixed inset-0 bg-black/60 flex items-center justify-center z-50"
          role="dialog"
          aria-modal="true"
          aria-labelledby="rotate-domain-title"
        >
          <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-6 w-96 max-w-full">
            <h3 id="rotate-domain-title" className="text-lg font-semibold text-[#E8EAF0] mb-2">
              Rotate DKIM Key
            </h3>
            <p className="text-sm text-[#8B92A8] mb-1">
              Rotate the DKIM key for <span className="text-[#E8EAF0] font-mono">{confirmRotate.domain.name}</span>?
            </p>
            <p className="text-sm text-[#FBBF24] mb-4">
              After rotation, you must update the DNS TXT record with the new key. DKIM verification will fail until the new record propagates.
            </p>
            {rotateDKIM.isPending && (
              <p className="text-[#8B92A8] text-xs mb-3">Generating a new RSA-2048 key pair…</p>
            )}
            {rotateDKIM.isError && (
              <p className="text-[#F87171] text-xs mb-3" role="alert">
                {errorMessage(rotateDKIM.error, "Rotation failed.")}
              </p>
            )}
            <div className="flex gap-3 justify-end">
              <button onClick={() => setConfirmRotate(null)}
                disabled={rotateDKIM.isPending}
                className="px-4 py-2 text-sm text-[#8B92A8] hover:text-[#E8EAF0] rounded-lg border border-[#2A2F3E] disabled:opacity-50">
                Cancel
              </button>
              <button
                onClick={() => { if (!rotateDKIM.isPending) rotateDKIM.mutate(confirmRotate.domain); }}
                disabled={rotateDKIM.isPending}
                className="px-4 py-2 text-sm rounded-lg bg-[#4F7CFF] text-white hover:bg-[#3B5FD9] disabled:opacity-50">
                {rotateDKIM.isPending ? "Rotating…" : "Rotate Key"}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* DNS Health Drawer */}
      {dnsPanel && (
        <DNSHealthDrawer
          domain={dnsPanel}
          onClose={() => setDnsPanel(null)}
        />
      )}

      <div className="flex justify-between items-center mb-6">
        <h2 className="text-2xl font-semibold text-[#E8EAF0]">Domain Management</h2>
        <button onClick={() => setShowCreate((v) => !v)}
          className="inline-flex items-center gap-2 px-4 py-2 bg-[#4F7CFF] text-white rounded-lg text-sm hover:bg-[#3B5FD9]">
          <Plus size={16} /> Add Domain
        </button>
      </div>

      {showCreate && (
        <form
          onSubmit={(e) => { e.preventDefault(); if (newDomain.trim()) createMutation.mutate(newDomain.trim()); }}
          className="flex gap-2 mb-4 bg-[#13161C] border border-[#2A2F3E] rounded-lg p-3"
        >
          <input
            type="text"
            required
            placeholder="example.com"
            value={newDomain}
            onChange={(e) => setNewDomain(e.target.value)}
            aria-label="New domain name"
            className="flex-1 px-3 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded-lg text-[#E8EAF0] text-sm font-mono"
          />
          <button type="submit" disabled={createMutation.isPending}
            className="px-4 py-2 bg-[#4F7CFF] text-white rounded-lg text-sm hover:bg-[#3B5FD9] disabled:opacity-50">
            {createMutation.isPending ? "Adding..." : "Add"}
          </button>
        </form>
      )}
      {createMutation.isError && (
        <p className="text-[#F87171] text-sm mb-4" role="alert">
          {errorMessage(createMutation.error, "Failed to add domain.")}
        </p>
      )}

      <input type="text" placeholder="Search domains..." value={search}
        onChange={(e) => setSearch(e.target.value)}
        className="w-full px-3 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded-lg text-[#E8EAF0] text-sm mb-4" />

      {filtered.length === 0 ? (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-8 text-center">
          <p className="text-[#8B92A8]">{items.length === 0 ? "No domains configured yet." : "No domains match your search."}</p>
        </div>
      ) : (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[#2A2F3E]">
                <th className="text-left p-4 text-[#8B92A8] font-medium">Domain</th>
                <th className="text-left p-4 text-[#8B92A8] font-medium">Status</th>
                <th className="text-left p-4 text-[#8B92A8] font-medium">Plan</th>
                <th className="text-right p-4 text-[#8B92A8] font-medium">Mailboxes</th>
                <th className="text-right p-4 text-[#8B92A8] font-medium">DKIM</th>
                <th className="text-right p-4 text-[#8B92A8] font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((d) => (
                <tr key={d.id} className="border-b border-[#2A2F3E] hover:bg-[#1A1E26]">
                  <td className="p-4">
                    <button
                      onClick={() => setDnsPanel(d)}
                      onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); setDnsPanel(d); } }}
                      className="text-[#E8EAF0] font-medium font-mono text-xs hover:text-[#4F7CFF] hover:underline text-left"
                      role="button"
                      tabIndex={0}
                    >
                      {d.name}
                    </button>
                  </td>
                  <td className="p-4"><StatusBadge status={d.status} /></td>
                  <td className="p-4 text-[#8B92A8] text-xs">{d.plan || "—"}</td>
                  <td className="p-4 text-right text-[#8B92A8] text-xs">{d.mailbox_count ?? "—"}</td>
                  <td className="p-4 text-right text-[#8B92A8] text-xs">
                    {d.dkim_enabled ? (
                      <button
                        onClick={() => setConfirmRotate({ domain: d })}
                        disabled={rotateDKIM.isPending}
                        className="text-[#34D399] hover:underline disabled:opacity-50 inline-flex items-center gap-1">
                        <RefreshCw size={12} className={rotateDKIM.isPending ? "animate-spin" : ""} /> Rotate
                      </button>
                    ) : (
                      <button
                        onClick={() => generateDKIM.mutate(d)}
                        disabled={generateDKIM.isPending}
                        className="text-[#4F7CFF] hover:underline disabled:opacity-50">
                        Generate
                      </button>
                    )}
                  </td>
                  <td className="p-4 text-right">
                    <button
                      onClick={() => setConfirmDelete({ domain: d })}
                      className="text-xs text-[#F87171] hover:underline inline-flex items-center gap-1">
                      <Trash2 size={12} /> Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {/* Show DNS instructions after successful rotation */}
          {Object.entries(dkimR).filter(([, r]) => r != null).map(([idKey, r]) => (
            <div key={`dkim-${idKey}`} className="p-4 border-t border-[#2A2F3E]">
              <DKIMInstructions result={r!} />
              <button onClick={() => dismissDkim(Number(idKey))} className="mt-2 text-xs text-[#8B92A8] hover:text-[#E8EAF0]">Dismiss</button>
            </div>
          ))}
          {/* Show DKIM error messages */}
          {Object.entries(dkimErr).filter(([, e]) => e != null).map(([idKey, e]) => (
            <div key={`dkim-err-${idKey}`} className="p-4 border-t border-[#2A2F3E]">
              <p className="text-[#F87171] text-xs" role="alert">{e}</p>
              <button onClick={() => dismissDkim(Number(idKey))} className="mt-1 text-xs text-[#8B92A8] hover:text-[#E8EAF0]">Dismiss</button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
