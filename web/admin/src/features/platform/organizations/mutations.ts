import { useMutation, useQueryClient } from "@tanstack/react-query";
import { setOrganizationActive, updateOrganization } from "./api";
import type { UpdateOrganizationRequest } from "./contract";

export function useSetOrganizationActiveMutation(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (active: boolean) => setOrganizationActive(id, { active }),
    onSuccess: () => {
      // The mutation response is just {"status":"ok"} — reload
      // authoritative state from the server rather than assuming the
      // new active value locally.
      qc.invalidateQueries({ queryKey: ["platform-organization-detail", id] });
      qc.invalidateQueries({ queryKey: ["platform-organizations"] });
    },
  });
}

export function useUpdateOrganizationMutation(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: UpdateOrganizationRequest) => updateOrganization(id, body),
    onSuccess: () => {
      // The response is the base Organization shape, not the Detail
      // shape (no domain_count/mailbox_count/admin_count/
      // quota_used_bytes/status_label) — always reload the detail
      // query rather than reading those fields off this response.
      qc.invalidateQueries({ queryKey: ["platform-organization-detail", id] });
      qc.invalidateQueries({ queryKey: ["platform-organizations"] });
    },
  });
}
