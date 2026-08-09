import { useMutation, useQueryClient } from "@tanstack/react-query";
import * as api from "./api";
import type { CreateLogRuleRequest } from "./contract";

export function useReloadSslCertificatesMutation() {
  const qc = useQueryClient();
  return useMutation({ mutationFn: api.reloadSslCertificates, onSuccess: () => qc.invalidateQueries({ queryKey: ["ssl-certs"] }) });
}
export function useDeleteSslCertificateMutation() {
  const qc = useQueryClient();
  return useMutation({ mutationFn: (id: string) => api.deleteSslCertificate(id), onSuccess: () => qc.invalidateQueries({ queryKey: ["ssl-certs"] }) });
}

export function useRunHealCheckMutation() {
  const qc = useQueryClient();
  return useMutation({ mutationFn: (name: string) => api.runHealCheck(name), onSuccess: () => qc.invalidateQueries({ queryKey: ["heal-history"] }) });
}

export function useCreateLogRuleMutation() {
  const qc = useQueryClient();
  return useMutation({ mutationFn: (body: CreateLogRuleRequest) => api.createLogRule(body), onSuccess: () => qc.invalidateQueries({ queryKey: ["log-rules"] }) });
}
export function useDeleteLogRuleMutation() {
  const qc = useQueryClient();
  return useMutation({ mutationFn: (id: number) => api.deleteLogRule(id), onSuccess: () => qc.invalidateQueries({ queryKey: ["log-rules"] }) });
}
