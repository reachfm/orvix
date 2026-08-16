// HTTP transport only, through the shared CSRF/auth-aware client.
// Every call targets an explicit tenant id in the route path — no
// support-context header, no grant, no impersonation.
import { request } from "../../../api";
import type {
  DeactivatePlatformDomainRequest,
  DeactivatePlatformDomainResponse,
  PlatformCreateDomainRequest,
  PlatformCreateDomainResult,
  PlatformDKIMMutationRequest,
  PlatformDKIMMutationResult,
  PlatformDomain,
  PlatformDomainDNSResult,
  PlatformDomainFilter,
  PlatformDomainList,
  SetPlatformDomainMailAccessModeRequest,
  SetPlatformDomainMailAccessModeResponse,
  SetPlatformDomainStatusRequest,
  SetPlatformDomainStatusResponse,
} from "./contract";

export function listPlatformDomains(tenantId: number, filter: PlatformDomainFilter): Promise<PlatformDomainList> {
  const params = new URLSearchParams();
  if (filter.q) params.set("q", filter.q);
  if (filter.status) params.set("status", filter.status);
  if (filter.limit !== undefined) params.set("limit", String(filter.limit));
  if (filter.offset !== undefined) params.set("offset", String(filter.offset));
  const qs = params.toString();
  return request<PlatformDomainList>(`/platform/domains/${tenantId}${qs ? "?" + qs : ""}`);
}

export function getPlatformDomain(tenantId: number, id: number): Promise<PlatformDomain> {
  return request<PlatformDomain>(`/platform/domains/${tenantId}/${id}`);
}

export function setPlatformDomainStatus(
  tenantId: number,
  id: number,
  body: SetPlatformDomainStatusRequest,
): Promise<SetPlatformDomainStatusResponse> {
  return request<SetPlatformDomainStatusResponse>(`/platform/domains/${tenantId}/${id}/status`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function createPlatformDomain(
  tenantId: number,
  body: PlatformCreateDomainRequest,
  idempotencyKey: string,
): Promise<PlatformCreateDomainResult> {
  return request<PlatformCreateDomainResult>(`/platform/domains/${tenantId}`, {
    method: "POST",
    body: JSON.stringify(body),
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

export function setPlatformDomainMailAccessMode(
  tenantId: number,
  id: number,
  body: SetPlatformDomainMailAccessModeRequest,
): Promise<SetPlatformDomainMailAccessModeResponse> {
  return request<SetPlatformDomainMailAccessModeResponse>(`/platform/domains/${tenantId}/${id}/mail-access-mode`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

/** Read-only existing-domain DNS/DKIM snapshot: GET .../dns. */
export function getPlatformDomainDNS(tenantId: number, id: number): Promise<PlatformDomainDNSResult> {
  return request<PlatformDomainDNSResult>(`/platform/domains/${tenantId}/${id}/dns`);
}

/** Provisions a new DKIM key pair for a domain that does not already have one. */
export function generatePlatformDomainDKIM(
  tenantId: number,
  id: number,
  body: PlatformDKIMMutationRequest,
  idempotencyKey: string,
): Promise<PlatformDKIMMutationResult> {
  return request<PlatformDKIMMutationResult>(`/platform/domains/${tenantId}/${id}/dkim/generate`, {
    method: "POST",
    body: JSON.stringify(body),
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

/** Replaces an existing DKIM key pair. body.confirm_rotation must be "rotate-dkim-key". */
export function rotatePlatformDomainDKIM(
  tenantId: number,
  id: number,
  body: PlatformDKIMMutationRequest,
  idempotencyKey: string,
): Promise<PlatformDKIMMutationResult> {
  return request<PlatformDKIMMutationResult>(`/platform/domains/${tenantId}/${id}/dkim/rotate`, {
    method: "POST",
    body: JSON.stringify(body),
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

/** Canonical, audited platform domain deactivate/soft-delete. Never a hard delete. */
export function deactivatePlatformDomain(
  tenantId: number,
  id: number,
  body: DeactivatePlatformDomainRequest,
  idempotencyKey: string,
): Promise<DeactivatePlatformDomainResponse> {
  return request<DeactivatePlatformDomainResponse>(`/platform/domains/${tenantId}/${id}/deactivate`, {
    method: "POST",
    body: JSON.stringify(body),
    headers: { "Idempotency-Key": idempotencyKey },
  });
}
