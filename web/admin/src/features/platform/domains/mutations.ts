import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createPlatformDomain, setPlatformDomainMailAccessMode, setPlatformDomainStatus } from "./api";
import type { MailAccessMode, PlatformCreateDomainRequest } from "./contract";

export function domainInvalidationKeys() {
  return [
    ["platform-domains"],
    ["overview"],
    ["platform-audit"],
    ["platform-mailboxes"],
  ] as const;
}

/**
 * Creates a domain for an explicit tenant. The caller supplies a
 * stable idempotencyKey per submission ATTEMPT — the same key must be
 * reused when retrying the identical request, and a fresh key must be
 * generated for a new or changed request (see CreateDomainDialog).
 */
export function useCreateDomainMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (args: { data: PlatformCreateDomainRequest; idempotencyKey: string }) =>
      createPlatformDomain(tenantId as number, args.data, args.idempotencyKey),
    onSuccess: () => {
      for (const key of domainInvalidationKeys()) qc.invalidateQueries({ queryKey: key });
    },
  });
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
