import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  createPlatformDomain,
  deactivatePlatformDomain,
  generatePlatformDomainDKIM,
  rotatePlatformDomainDKIM,
  setPlatformDomainMailAccessMode,
  setPlatformDomainStatus,
} from "./api";
import { domainKeys } from "./queries";
import type {
  DeactivatePlatformDomainRequest,
  MailAccessMode,
  PlatformCreateDomainRequest,
  PlatformDKIMMutationRequest,
} from "./contract";

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

/**
 * Provisions a new DKIM key pair for an existing domain that does not
 * already have one. On success, refetches domain detail, the live DNS
 * snapshot, and the domain list — the caller never assumes the new
 * state before the backend confirms it.
 */
export function useGenerateDKIMMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body, idempotencyKey }: { id: number; body: PlatformDKIMMutationRequest; idempotencyKey: string }) =>
      generatePlatformDomainDKIM(tenantId as number, id, body, idempotencyKey),
    onSuccess: (_result, { id }) => {
      qc.invalidateQueries({ queryKey: domainKeys.detail(tenantId, id) });
      qc.invalidateQueries({ queryKey: domainKeys.dns(tenantId, id) });
      for (const key of domainInvalidationKeys()) qc.invalidateQueries({ queryKey: key });
    },
  });
}

/**
 * Replaces an existing DKIM key pair. body.confirm_rotation must be
 * "rotate-dkim-key". On success, refetches domain detail and the live
 * DNS snapshot so the newly rotated public value is rendered — never
 * an old TXT value retained as if it were current.
 */
export function useRotateDKIMMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body, idempotencyKey }: { id: number; body: PlatformDKIMMutationRequest; idempotencyKey: string }) =>
      rotatePlatformDomainDKIM(tenantId as number, id, body, idempotencyKey),
    onSuccess: (_result, { id }) => {
      qc.invalidateQueries({ queryKey: domainKeys.detail(tenantId, id) });
      qc.invalidateQueries({ queryKey: domainKeys.dns(tenantId, id) });
      for (const key of domainInvalidationKeys()) qc.invalidateQueries({ queryKey: key });
    },
  });
}

/**
 * Canonical, audited platform domain deactivate/soft-delete. Requires
 * the real currently-loaded domain.version as expected_version and the
 * exact typed confirmation phrase (see deactivateDomainConfirmation).
 * On success, invalidates list/detail/DNS state so the row never stays
 * visually Active.
 */
export function useDeactivateDomainMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body, idempotencyKey }: { id: number; body: DeactivatePlatformDomainRequest; idempotencyKey: string }) =>
      deactivatePlatformDomain(tenantId as number, id, body, idempotencyKey),
    onSuccess: (_result, { id }) => {
      qc.invalidateQueries({ queryKey: domainKeys.detail(tenantId, id) });
      qc.invalidateQueries({ queryKey: domainKeys.dns(tenantId, id) });
      for (const key of domainInvalidationKeys()) qc.invalidateQueries({ queryKey: key });
    },
  });
}
