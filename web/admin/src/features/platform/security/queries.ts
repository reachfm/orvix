import { useQuery } from "@tanstack/react-query";
import * as api from "./api";

export function useAuditLogsQuery() {
  return useQuery({ queryKey: ["platform-audit"], queryFn: api.listAuditLogs, retry: false });
}
export function useSslCertificatesQuery() {
  return useQuery({ queryKey: ["ssl-certs"], queryFn: api.listSslCertificates, retry: false });
}
export function useAcmeStatusQuery() {
  return useQuery({ queryKey: ["acme-status"], queryFn: api.getAcmeStatus, retry: false });
}
export function useSslExpiryWarningsQuery() {
  return useQuery({ queryKey: ["ssl-warnings"], queryFn: api.getSslExpiryWarnings, retry: false });
}
export function useAntivirusStatusQuery() {
  return useQuery({ queryKey: ["antivirus-status"], queryFn: api.getAntivirusStatus, retry: false });
}
export function useFirewallRulesQuery() {
  return useQuery({ queryKey: ["firewall-rules"], queryFn: api.listFirewallRules, retry: false });
}
export function useFirewallLogsQuery() {
  return useQuery({ queryKey: ["firewall-logs"], queryFn: api.listFirewallLogs, retry: false });
}
export function useGuardianLogsQuery() {
  return useQuery({ queryKey: ["guardian-logs"], queryFn: api.listGuardianLogs, retry: false });
}
export function useHealHistoryQuery() {
  return useQuery({ queryKey: ["heal-history"], queryFn: api.listHealHistory, retry: false });
}
export function useLogRulesQuery() {
  return useQuery({ queryKey: ["log-rules"], queryFn: api.listLogRules, retry: false });
}
