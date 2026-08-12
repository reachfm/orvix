import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createIncident, getIncident, getIncidentTimeline, listIncidents, updateIncident } from "./api";
import type { CreateIncidentRequest, UpdateIncidentRequest } from "./contract";

export const incidentKeys = {
  list: ["incidents", "list"] as const,
  detail: (id: number) => ["incidents", "detail", id] as const,
  timeline: (id: number) => ["incidents", "timeline", id] as const,
};

export function useIncidents(status?: string) {
  return useQuery({ queryKey: ["incidents", "list", status ?? ""], queryFn: () => listIncidents(status) });
}

export function useIncidentDetail(id: number) {
  return useQuery({ queryKey: incidentKeys.detail(id), queryFn: () => getIncident(id) });
}

export function useIncidentTimeline(id: number) {
  return useQuery({ queryKey: incidentKeys.timeline(id), queryFn: () => getIncidentTimeline(id) });
}

function useInvalidate(id: number) {
  const qc = useQueryClient();
  return () => {
    qc.invalidateQueries({ queryKey: ["incidents", "list"] });
    qc.invalidateQueries({ queryKey: incidentKeys.detail(id) });
    qc.invalidateQueries({ queryKey: incidentKeys.timeline(id) });
  };
}

export function useCreateIncident() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateIncidentRequest) => createIncident(data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["incidents", "list"] }),
  });
}

export function useUpdateIncident(id: number) {
  const invalidate = useInvalidate(id);
  return useMutation({ mutationFn: (data: UpdateIncidentRequest) => updateIncident(id, data), onSuccess: invalidate });
}
