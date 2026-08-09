import { useMutation, useQueryClient } from "@tanstack/react-query";
import { setOrganizationActive } from "./api";

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
