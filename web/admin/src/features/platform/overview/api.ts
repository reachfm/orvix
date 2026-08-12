import { request } from "../../../api";
import type { PlatformDashboard } from "./contract";

export function getPlatformDashboard(): Promise<PlatformDashboard> {
  return request<PlatformDashboard>("/platform/dashboard");
}
