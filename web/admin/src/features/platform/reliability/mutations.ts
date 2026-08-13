import { useMutation, useQueryClient } from "@tanstack/react-query";
import * as api from "./api";

function invalidateBackups(qc: ReturnType<typeof useQueryClient>) {
  qc.invalidateQueries({ queryKey: ["backups"] });
  qc.invalidateQueries({ queryKey: ["backup-metrics"] });
  qc.invalidateQueries({ queryKey: ["backup-health"] });
}

export function useCreateBackupMutation() {
  const qc = useQueryClient();
  return useMutation({ mutationFn: api.createBackup, onSuccess: () => invalidateBackups(qc) });
}
export function useRunBackupNowMutation() {
  const qc = useQueryClient();
  return useMutation({ mutationFn: api.runBackupNow, onSuccess: () => invalidateBackups(qc) });
}
export function useValidateBackupMutation() {
  const qc = useQueryClient();
  return useMutation({ mutationFn: (id: string) => api.validateBackup(id), onSuccess: () => invalidateBackups(qc) });
}
export function useRestoreBackupMutation() {
  return useMutation({ mutationFn: (id: string) => api.restoreBackup(id) });
}
export function useDeleteBackupMutation() {
  const qc = useQueryClient();
  return useMutation({ mutationFn: (id: string) => api.deleteBackup(id), onSuccess: () => invalidateBackups(qc) });
}
export function useRunBackupRetentionMutation() {
  const qc = useQueryClient();
  return useMutation({ mutationFn: api.runBackupRetention, onSuccess: () => invalidateBackups(qc) });
}

export function useCheckForUpdateMutation() {
  const qc = useQueryClient();
  return useMutation({ mutationFn: api.postUpdateCheck, onSuccess: () => qc.invalidateQueries({ queryKey: ["update-check"] }) });
}
export function useRunUpdateMutation() {
  const qc = useQueryClient();
  return useMutation({ mutationFn: api.runUpdate, onSuccess: () => qc.invalidateQueries({ queryKey: ["update-status"] }) });
}

export function useResolveAlertMutation() {
  const qc = useQueryClient();
  return useMutation({ mutationFn: (id: number) => api.resolveMonitoringAlert(id), onSuccess: () => qc.invalidateQueries({ queryKey: ["mon-alerts"] }) });
}
