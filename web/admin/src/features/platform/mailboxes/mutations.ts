import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  createPlatformMailbox,
  deletePlatformMailbox,
  endMailboxSupportView,
  resetPlatformMailboxPassword,
  setPlatformMailboxAccessMode,
  setPlatformMailboxQuota,
  setPlatformMailboxStatus,
  startMailboxSupportView,
} from "./api";
import type { PlatformCreateMailboxRequest, MailAccessMode, StartMailboxSupportViewRequest } from "./contract";

export function mailboxInvalidationKeys() {
  return [
    ["platform-mailboxes"],
    ["platform-domains"],
    ["overview"],
    ["platform-audit"],
    ["platform-usage"],
  ] as const;
}

/**
 * Creates a mailbox for an explicit tenant. The caller supplies a
 * stable idempotencyKey per submission ATTEMPT — reused across
 * retries of the identical request, replaced on any field change.
 */
export function useCreateMailboxMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (args: { data: PlatformCreateMailboxRequest; idempotencyKey: string }) =>
      createPlatformMailbox(tenantId as number, args.data, args.idempotencyKey),
    onSuccess: () => {
      for (const key of mailboxInvalidationKeys()) qc.invalidateQueries({ queryKey: key });
    },
  });
}

/**
 * Guarded access-mode mutation. On a version conflict the mailbox was
 * changed elsewhere since it was last read — the caller MUST refetch
 * (this hook invalidates the detail/list queries even on error) and
 * must never blindly retry with the same stale expected_version.
 */
export function useSetMailboxAccessModeMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (args: { id: number; mailAccessMode: MailAccessMode; expectedVersion: number; idempotencyKey: string }) =>
      setPlatformMailboxAccessMode(
        tenantId as number,
        args.id,
        { mail_access_mode: args.mailAccessMode, expected_version: args.expectedVersion },
        args.idempotencyKey,
      ),
    onSuccess: () => {
      for (const key of mailboxInvalidationKeys()) qc.invalidateQueries({ queryKey: key });
    },
    onError: () => {
      // A conflict (or any other failure) means our locally-held
      // version may be stale — refetch rather than let the UI keep
      // offering a mutation against a value that no longer matches
      // the server.
      qc.invalidateQueries({ queryKey: ["platform-mailboxes"] });
    },
  });
}

export function useSetMailboxStatusMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, status, reason }: { id: number; status: string; reason?: string }) =>
      setPlatformMailboxStatus(tenantId as number, id, { status, reason: reason ?? "" }),
    onSuccess: () => {
      for (const key of mailboxInvalidationKeys()) qc.invalidateQueries({ queryKey: key });
    },
  });
}

export function useSetMailboxQuotaMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, quotaMb }: { id: number; quotaMb: number }) =>
      setPlatformMailboxQuota(tenantId as number, id, { quota_mb: quotaMb }),
    onSuccess: () => {
      for (const key of mailboxInvalidationKeys()) qc.invalidateQueries({ queryKey: key });
    },
  });
}

export function useResetMailboxPasswordMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => resetPlatformMailboxPassword(tenantId as number, id),
    onSuccess: () => {
      for (const key of mailboxInvalidationKeys()) qc.invalidateQueries({ queryKey: key });
    },
  });
}

export function useDeleteMailboxMutation(tenantId: number | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, confirmation }: { id: number; confirmation: string }) =>
      deletePlatformMailbox(tenantId as number, id, confirmation),
    onSuccess: () => {
      for (const key of mailboxInvalidationKeys()) qc.invalidateQueries({ queryKey: key });
    },
  });
}

/**
 * Starts an audited, read-only, time-boxed mailbox support-view
 * session. Never mints a customer session/JWT and never reads/resets
 * the mailbox password — see internal/api/handlers/platform_mailbox_support_view.go.
 */
export function useStartMailboxSupportViewMutation(tenantId: number | null) {
  return useMutation({
    mutationFn: ({ id, body }: { id: number; body: StartMailboxSupportViewRequest }) =>
      startMailboxSupportView(tenantId as number, id, body),
  });
}

export function useEndMailboxSupportViewMutation(tenantId: number | null) {
  return useMutation({
    mutationFn: ({ id, sessionId }: { id: number; sessionId: string }) => endMailboxSupportView(tenantId as number, id, sessionId),
  });
}
