import { useMutation, useQueryClient, type QueryClient } from "@tanstack/react-query";
import {
  createPlatformRelay,
  deletePlatformRelay,
  disablePlatformRelay,
  enablePlatformRelay,
  newIdempotencyKey,
  rotatePlatformRelayCredentials,
  testPlatformRelay,
  updatePlatformRelay,
} from "./api";
import type {
  CreatePlatformRelayRequest,
  UpdatePlatformRelayRequest,
} from "./contract";

export function relayInvalidationKeys() {
  return [
    ["platform-relays"],
    ["overview"],
    ["platform-audit"],
    ["mail-operations"],
  ] as const;
}

export function invalidateRelays(qc: QueryClient) {
  for (const key of relayInvalidationKeys()) qc.invalidateQueries({ queryKey: key });
}

export function useCreatePlatformRelayMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ body, idempotencyKey }: { body: CreatePlatformRelayRequest; idempotencyKey: string }) =>
      createPlatformRelay(body, idempotencyKey),
    onSuccess: () => invalidateRelays(qc),
  });
}

export function useUpdatePlatformRelayMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body, idempotencyKey }: { id: number; body: UpdatePlatformRelayRequest; idempotencyKey: string }) =>
      updatePlatformRelay(id, body, idempotencyKey),
    onSuccess: () => invalidateRelays(qc),
  });
}

export function useEnablePlatformRelayMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, version, idempotencyKey }: { id: number; version: number; idempotencyKey: string }) =>
      enablePlatformRelay(id, { version }, idempotencyKey),
    onSuccess: () => invalidateRelays(qc),
  });
}

export function useDisablePlatformRelayMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, version, idempotencyKey, confirmation }: { id: number; version: number; idempotencyKey: string; confirmation: string }) =>
      disablePlatformRelay(id, { version }, idempotencyKey, confirmation),
    onSuccess: () => invalidateRelays(qc),
  });
}

export function useRotatePlatformRelayCredentialsMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, version, newPassword, idempotencyKey, confirmation }: { id: number; version: number; newPassword?: string; idempotencyKey: string; confirmation: string }) =>
      rotatePlatformRelayCredentials(id, { version, new_password: newPassword }, idempotencyKey, confirmation),
    onSuccess: () => invalidateRelays(qc),
  });
}

export function useTestPlatformRelayMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, idempotencyKey }: { id: number; idempotencyKey: string }) =>
      testPlatformRelay(id, idempotencyKey),
    onSuccess: () => invalidateRelays(qc),
  });
}

export function useDeletePlatformRelayMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, confirmation }: { id: number; confirmation: string }) =>
      deletePlatformRelay(id, confirmation),
    onSuccess: () => invalidateRelays(qc),
  });
}

export { newIdempotencyKey };
