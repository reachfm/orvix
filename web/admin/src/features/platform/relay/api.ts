// HTTP transport only, through the shared CSRF/auth-aware client.
// Every mutation carries an Idempotency-Key header (the backend
// requires it via platformIdempotent); destructive/credential actions
// carry the X-Confirm typed-confirmation header. Secrets are only ever
// sent inside request bodies — never in URLs, query strings, or
// response caching.
import { request } from "../../../api";
import type {
  CreatePlatformRelayRequest,
  DeleteRelayResponse,
  ListPlatformRelaysResponse,
  PlatformRelay,
  PlatformRelayFilter,
  RelayActiveRequest,
  RelayHealthCheckResult,
  RotateRelayCredentialsRequest,
  RotateRelayCredentialsResponse,
  UpdatePlatformRelayRequest,
} from "./contract";

/** Fresh idempotency key per logical mutation (retry-safe, never reused across operations). */
export function newIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `relay-${Date.now()}-${Math.random().toString(36).slice(2, 12)}`;
}

export function listPlatformRelays(filter: PlatformRelayFilter): Promise<ListPlatformRelaysResponse> {
  const params = new URLSearchParams();
  if (filter.scope) params.set("scope", filter.scope);
  if (filter.q) params.set("q", filter.q);
  if (filter.tenant_id !== undefined) params.set("tenant_id", String(filter.tenant_id));
  if (filter.domain_id !== undefined) params.set("domain_id", String(filter.domain_id));
  if (filter.active !== undefined) params.set("active", String(filter.active));
  if (filter.limit !== undefined) params.set("limit", String(filter.limit));
  if (filter.offset !== undefined) params.set("offset", String(filter.offset));
  const qs = params.toString();
  return request<ListPlatformRelaysResponse>(`/platform/relays${qs ? "?" + qs : ""}`);
}

export function getPlatformRelay(id: number): Promise<PlatformRelay> {
  return request<PlatformRelay>(`/platform/relays/${id}`);
}

export function createPlatformRelay(body: CreatePlatformRelayRequest, idempotencyKey: string): Promise<PlatformRelay> {
  return request<PlatformRelay>("/platform/relays", {
    method: "POST",
    body: JSON.stringify(body),
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

export function updatePlatformRelay(id: number, body: UpdatePlatformRelayRequest, idempotencyKey: string): Promise<PlatformRelay> {
  return request<PlatformRelay>(`/platform/relays/${id}`, {
    method: "PATCH",
    body: JSON.stringify(body),
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

export function enablePlatformRelay(id: number, body: RelayActiveRequest, idempotencyKey: string): Promise<PlatformRelay> {
  return request<PlatformRelay>(`/platform/relays/${id}/enable`, {
    method: "POST",
    body: JSON.stringify(body),
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

export function disablePlatformRelay(id: number, body: RelayActiveRequest, idempotencyKey: string, confirmation: string): Promise<PlatformRelay> {
  return request<PlatformRelay>(`/platform/relays/${id}/disable`, {
    method: "POST",
    body: JSON.stringify(body),
    headers: { "Idempotency-Key": idempotencyKey, "X-Confirm": confirmation },
  });
}

export function rotatePlatformRelayCredentials(
  id: number,
  body: RotateRelayCredentialsRequest,
  idempotencyKey: string,
  confirmation: string,
): Promise<RotateRelayCredentialsResponse> {
  return request<RotateRelayCredentialsResponse>(`/platform/relays/${id}/rotate-credentials`, {
    method: "POST",
    body: JSON.stringify(body),
    headers: { "Idempotency-Key": idempotencyKey, "X-Confirm": confirmation },
  });
}

export function testPlatformRelay(id: number, idempotencyKey: string): Promise<RelayHealthCheckResult> {
  return request<RelayHealthCheckResult>(`/platform/relays/${id}/test`, {
    method: "POST",
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

export function deletePlatformRelay(id: number, confirmation: string): Promise<DeleteRelayResponse> {
  return request<DeleteRelayResponse>(`/platform/relays/${id}`, {
    method: "DELETE",
    headers: { "X-Confirm": confirmation },
  });
}
