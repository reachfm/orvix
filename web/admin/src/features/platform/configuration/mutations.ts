import { useMutation, useQueryClient } from "@tanstack/react-query";
import { patchAdminSettings, updateFeatureFlag } from "./api";
import type { SettingsPatchRequest } from "./contract";

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
