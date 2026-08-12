import { useMutation, useQueryClient } from "@tanstack/react-query";
import { setPlatformDomainMailAccessMode, setPlatformDomainStatus } from "./api";
import type { MailAccessMode } from "./contract";

export function domainInvalidationKeys() {
  return [
    ["platform-domains"],
    ["overview"],
    ["platform-audit"],
    ["platform-mailboxes"],
  ] as const;
}

/** Applies a lifecycle status transition for a domain of an explicit tenant. */
export function useSetDomainStatusMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, status, reason }: { id: number; status: string; reason?: string }) =>
      setPlatformDomainStatus(tenantId as number, id, { status, reason: reason ?? "" }),
    onSuccess: () => {
      // The backend response is a confirmation envelope ({status, id}),
      // not the updated domain — invalidate list/detail/overview/audit
      // and the mailbox inventory (domain lifecycle affects eligibility).
      for (const key of domainInvalidationKeys()) qc.invalidateQueries({ queryKey: key });
    },
  });
}

/** Sets the canonical mail-access mode (internal_only | internal_external). */
export function useSetMailAccessModeMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, mailAccessMode }: { id: number; mailAccessMode: MailAccessMode }) =>
      setPlatformDomainMailAccessMode(tenantId as number, id, { mail_access_mode: mailAccessMode }),
    onSuccess: () => {
      for (const key of domainInvalidationKeys()) qc.invalidateQueries({ queryKey: key });
    },
  });
}
