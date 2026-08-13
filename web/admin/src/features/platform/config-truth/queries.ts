import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { listCapabilities, listSettings, mutateSetting } from "./api";
import type { MutateSettingRequest } from "./contract";

export const configKeys = {
  settings: ["config-truth", "settings"] as const,
  capabilities: ["config-truth", "capabilities"] as const,
};

export function useSettings() {
  return useQuery({ queryKey: configKeys.settings, queryFn: () => listSettings() });
}

export function useCapabilities() {
  return useQuery({ queryKey: configKeys.capabilities, queryFn: () => listCapabilities() });
}

export function useMutateSetting() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (args: { key: string; data: MutateSettingRequest }) => mutateSetting(args.key, args.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["config-truth", "settings"] }),
  });
}
