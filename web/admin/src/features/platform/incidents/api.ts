import { request } from "../../../api";
import type { CreateIncidentRequest, Incident, IncidentDetailResponse, ListIncidentsResponse, TimelineEvent, UpdateIncidentRequest } from "./contract";

export function listIncidents(status?: string): Promise<ListIncidentsResponse> {
  const qs = status ? `?status=${status}` : "";
  return request<ListIncidentsResponse>(`/incidents${qs}`);
}

export function getIncident(id: number): Promise<Incident> {
  return request<Incident>(`/incidents/${id}`);
}

export function getIncidentTimeline(id: number): Promise<TimelineEvent[]> {
  return request<TimelineEvent[]>(`/incidents/${id}/timeline`);
}

export function createIncident(data: CreateIncidentRequest): Promise<IncidentDetailResponse> {
  return request<IncidentDetailResponse>("/incidents", { method: "POST", body: JSON.stringify(data) });
}

export function updateIncident(id: number, data: UpdateIncidentRequest): Promise<IncidentDetailResponse> {
  return request<IncidentDetailResponse>(`/incidents/${id}`, { method: "PATCH", body: JSON.stringify(data) });
}
