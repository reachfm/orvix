import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { coordinatedBackup, getReadiness, listDrills, listOperations, recordDrill } from "./api";
import type { RecordDrillRequest } from "./contract";

export const drKeys = {
  readiness: ["dr", "readiness"] as const,
  drills: ["dr", "drills"] as const,
  operations: ["dr", "operations"] as const,
};

export function useReadiness() {
  return useQuery({ queryKey: drKeys.readiness, queryFn: () => getReadiness() });
}

export function useDrills() {
  return useQuery({ queryKey: drKeys.drills, queryFn: () => listDrills() });
}

export function useOperations(limit?: number, offset?: number) {
  return useQuery({ queryKey: ["dr", "operations", limit ?? 25, offset ?? 0], queryFn: () => listOperations(limit, offset) });
}

function useInvalidate() {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: ["dr"] });
}

export function useRecordDrill() {
  const invalidate = useInvalidate();
  return useMutation({ mutationFn: (data: RecordDrillRequest) => recordDrill(data), onSuccess: invalidate });
}

export function useCoordinatedBackup() {
  const invalidate = useInvalidate();
  return useMutation({ mutationFn: (idempotencyKey: string) => coordinatedBackup(idempotencyKey), onSuccess: invalidate });
}
