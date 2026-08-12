import { useQuery } from "@tanstack/react-query";
import { getPlatformDashboard } from "./api";

export function usePlatformDashboardQuery() {
  return useQuery({
    queryKey: ["platform-dashboard"],
    queryFn: getPlatformDashboard,
    retry: false,
  });
}
