// Centralized, stable error mapping for the Platform Super Admin
// console (kernel error codes from internal/platform/kernel/errors.go
// plus the handler-level stable codes documented below).
//
// Every mail-control feature maps backend failures through this module.
// Raw SQL, SMTP, DNS, TLS, encryption, filesystem, and internal Go
// messages never reach the UI: kernel.AsAPIError redacts them to
// ErrCodeInternal on the backend, and this table renders a safe copy
// for every code. Unknown codes fall back to a safe generic message
// plus the server-provided text when present (never a raw stack).
import { ApiError } from "../../api";

export interface SafeErrorInfo {
  /** Stable backend code ("" when the server sent none). */
  code: string;
  /** Short, safe, operator-facing title. */
  title: string;
  /** Safe detail sentence. */
  detail: string;
  /** Optional request/correlation id surfaced by the backend. */
  requestId?: string;
}

const CODE_MESSAGES: Readonly<Record<string, { title: string; detail: string }>> = {
  // ── Auth / permission ─────────────────────────────────────────────
  UNAUTHORIZED: {
    title: "Authentication required",
    detail: "Your session could not be verified. Sign in again and retry the operation.",
  },
  FORBIDDEN: {
    title: "Permission denied",
    detail: "Your role does not permit this operation on this resource.",
  },
  // ── Resource / ownership ──────────────────────────────────────────
  NOT_FOUND: {
    title: "Not found",
    detail: "The requested resource does not exist or is not visible to this tenant.",
  },
  // ── Conflicts / state ─────────────────────────────────────────────
  CONFLICT: {
    title: "Conflict",
    detail: "The operation conflicts with the current state of the resource. Reload and retry.",
  },
  QUOTA_EXCEEDED: {
    title: "Limit reached",
    detail: "The operation exceeds a configured limit or quota for this resource.",
  },
  INVALID_STATE_TRANSITION: {
    title: "Invalid state transition",
    detail: "The resource is not in a state that allows this action.",
  },
  PRECONDITION_FAILED: {
    title: "Action requires confirmation",
    detail: "A current version or typed confirmation is required. Reload the resource and retry.",
  },
  IDEMPOTENCY_KEY_REUSE_MISMATCH: {
    title: "Retry conflict",
    detail: "A request with the same idempotency key was retried with different details. Reload and try again with a fresh action.",
  },
  VALIDATION_FAILED: {
    title: "Invalid input",
    detail: "One or more submitted values are invalid. Review the form and retry.",
  },
  // ── Infrastructure ────────────────────────────────────────────────
  UNAVAILABLE: {
    title: "Service unavailable",
    detail: "The required backend service is temporarily unavailable. Try again later.",
  },
  INTERNAL: {
    title: "Unexpected error",
    detail: "An unexpected error occurred. If a request ID is shown, include it in any support report.",
  },
  // ── Handler-level stable codes ────────────────────────────────────
  // CoreMail intentionally disabled (admin_queue.go:
  // coreMailUnavailableResponse).
  COREMAIL_DISABLED: {
    title: "Mail queue disabled",
    detail: "The mail queue is disabled on this deployment, so queue operations are unavailable.",
  },
  // queueActionError codes (admin_queue.go).
  invalid_state_transition: {
    title: "Invalid state transition",
    detail: "The queue message is not in a state that allows this action.",
  },
  not_found: {
    title: "Queue entry not found",
    detail: "The queue entry does not exist or was already removed.",
  },
  bad_request: {
    title: "Request rejected",
    detail: "The backend rejected the request. Reload the queue and retry.",
  },
  queue_unavailable: {
    title: "Queue unavailable",
    detail: "The queue backend is unavailable. Try again later.",
  },
  // Relay mutation guards (platform_relay_admin.go).
  STALE_VERSION: {
    title: "Stale version",
    detail: "The relay was modified by another request. Reload it and retry.",
  },
  RELAY_NAME_CONFLICT: {
    title: "Relay name conflict",
    detail: "A relay with this name already exists in the same scope.",
  },
  RELAY_UNSAFE_TARGET: {
    title: "Unsafe relay target",
    detail: "The relay host or port is not a permitted target.",
  },
  RELAY_TLS_FAILURE: {
    title: "Relay TLS failure",
    detail: "The relay TLS connection could not be established with the configured validation mode.",
  },
  RELAY_TIMEOUT: {
    title: "Relay timeout",
    detail: "The relay did not respond within the connection budget.",
  },
  RELAY_AUTH_FAILURE: {
    title: "Relay authentication failure",
    detail: "The relay rejected the configured credentials.",
  },
  // Duplicate-alias family (aliases).
  ALIAS_DUPLICATE: {
    title: "Duplicate alias",
    detail: "An alias with this source address already exists in the domain.",
  },
  ALIAS_DESTINATION_QUOTA: {
    title: "Destination quota exceeded",
    detail: "The alias destination has reached its limit.",
  },
  ALIAS_CROSS_TENANT_DESTINATION: {
    title: "Cross-tenant destination",
    detail: "The alias destination belongs to a different tenant and is not permitted.",
  },
  ALIAS_FORWARDING_LOOP: {
    title: "Forwarding loop",
    detail: "The alias chain would create a forwarding loop.",
  },
  ALIAS_SENDER_PERMISSION: {
    title: "Sender permission conflict",
    detail: "The alias conflicts with an existing sender-permission rule.",
  },
  SUPPRESSION_DUPLICATE: {
    title: "Duplicate suppression",
    detail: "An active suppression for this address already exists.",
  },
  SUPPRESSION_NOT_ACTIVE: {
    title: "Suppression not active",
    detail: "This suppression is not active, so the requested transition is not allowed.",
  },
  SUPPRESSION_ACTIVE: {
    title: "Suppression already active",
    detail: "This suppression is already active.",
  },
  DELIVERY_POLICY_DENIED: {
    title: "Delivery policy denied",
    detail: "The message was denied by the domain mail-access policy.",
  },
  DELIVERY_SUPPRESSED: {
    title: "Recipient suppressed",
    detail: "Delivery was blocked because the recipient is suppressed.",
  },
  GROUP_DUPLICATE_MEMBER: {
    title: "Duplicate member",
    detail: "The member is already part of the group.",
  },
  GROUP_LOOP: {
    title: "Group loop",
    detail: "The group membership would create a loop.",
  },
};

/** Extracts a stable backend error code from any thrown value. */
export function errorCodeOf(err: unknown): string {
  if (err instanceof ApiError) return err.code;
  return "";
}

/** Safe request id, when the backend surfaced one. */
export function requestIdOf(err: unknown): string | undefined {
  if (!(err instanceof ApiError) || !err.body || typeof err.body !== "object") return undefined;
  const body = err.body as Record<string, unknown>;
  const v = body.request_id ?? body.requestId ?? body.correlation_id;
  return typeof v === "string" && v.trim() ? v.trim() : undefined;
}

/**
 * Maps any thrown value to a safe, operator-facing message. Unknown
 * codes fall back to the server's own text ONLY when present, and
 * never to a raw stack/SQL/SMTP string (the backend already redacts
 * those to INTERNAL before they reach this client).
 */
export function safeErrorInfo(err: unknown, fallbackTitle = "Request failed"): SafeErrorInfo {
  const code = errorCodeOf(err);
  const requestId = requestIdOf(err);
  if (code && CODE_MESSAGES[code]) {
    return { code, ...CODE_MESSAGES[code], requestId };
  }
  const serverMessage =
    err instanceof ApiError ? (err.message && !err.message.startsWith("Request failed (") ? err.message : "") : "";
  return {
    code,
    title: fallbackTitle,
    detail: serverMessage && code === "INTERNAL" ? "An unexpected error occurred. If a request ID is shown, include it in any support report." : serverMessage || CODE_MESSAGES.INTERNAL.detail,
    requestId,
  };
}
