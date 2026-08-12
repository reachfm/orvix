// Exact contracts for the platform incident endpoints
// (internal/incident/domain.go + internal/api/handlers/incidents_support.go).

export type Severity = "critical" | "major" | "minor" | "degraded" | "scheduled";
export type IncidentStatus = "investigating" | "identified" | "monitoring" | "resolved" | "cancelled";

export const SEVERITIES: ReadonlyArray<Severity> = ["critical", "major", "minor", "degraded", "scheduled"];
export const INCIDENT_STATUSES: ReadonlyArray<IncidentStatus> = ["investigating", "identified", "monitoring", "resolved", "cancelled"];

export interface Incident {
  id: number;
  title: string;
  description?: string;
  severity: Severity;
  status: IncidentStatus;
  services?: string[];
  regions?: string[];
  tenant_ids?: number[];
  created_at: string;
  updated_at: string;
  resolved_at?: string;
}

export interface TimelineEvent {
  id: number;
  incident_id: number;
  status?: IncidentStatus;
  message: string;
  operator: string;
  created_at: string;
}

export interface ListIncidentsResponse {
  incidents: Incident[];
}

export interface IncidentDetailResponse {
  incident: Incident;
  timeline: TimelineEvent[];
}

export interface CreateIncidentRequest {
  title: string;
  description?: string;
  severity: Severity;
  services?: string[];
  regions?: string[];
}

export interface UpdateIncidentRequest {
  status: IncidentStatus;
  message: string;
}
