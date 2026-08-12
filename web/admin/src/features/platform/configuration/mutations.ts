import { useMutation, useQueryClient } from "@tanstack/react-query";
import { patchAdminSettings, updateFeatureFlag, patchProtocolSettings } from "./api";
import type { SettingsPatchRequest, ProtocolSettingsPatchRequest } from "./contract";

export function usePatchAdminSettingsMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: SettingsPatchRequest) => patchAdminSettings(body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admin-settings"] }),
  });
}

export function useUpdateFeatureFlagMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, enabled }: { id: number; enabled: boolean }) => updateFeatureFlag(id, enabled),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["feature-flags"] }),
  });
}

export function usePatchProtocolSettingsMutation(protocol: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ProtocolSettingsPatchRequest) => patchProtocolSettings(protocol, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["protocol-settings", protocol] }),
  });
}
