import { request } from "../../../api";
import type { MonitoringHealth } from "./contract";

export function getMonitoringHealth(): Promise<MonitoringHealth> {
  return request<MonitoringHealth>("/monitoring/health");
}
