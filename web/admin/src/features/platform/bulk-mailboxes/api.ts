// HTTP transport only — the production bulk endpoint is used directly;
// bulk is NEVER implemented by looping individual requests.
import { request } from "../../../api";
import type { BulkMailboxRequest, BulkMailboxResult } from "./contract";

export function bulkMailboxStatus(tenantId: number, body: BulkMailboxRequest): Promise<BulkMailboxResult> {
  return request<BulkMailboxResult>(`/platform/mailboxes/${tenantId}/bulk/status`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}
