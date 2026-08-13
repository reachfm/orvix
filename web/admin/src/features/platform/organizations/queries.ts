import { useQuery } from "@tanstack/react-query";
import { getOrganizationDetail, listOrganizations } from "./api";

export function useOrganizationsQuery(search: string, limit: number, offset: number) {
  return useQuery({
    queryKey: ["platform-organizations", search, limit, offset],
    queryFn: () => listOrganizations(search || undefined, limit, offset),
    retry: false,
  });
}

export function useOrganizationDetailQuery(id: number | null) {
  return useQuery({
    queryKey: ["platform-organization-detail", id],
    queryFn: () => getOrganizationDetail(id as number),
    enabled: id !== null,
    retry: false,
  });
}
