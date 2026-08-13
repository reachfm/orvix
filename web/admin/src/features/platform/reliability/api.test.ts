import { describe, it, expect, vi, afterEach } from "vitest";
import * as client from "../../../api";
import { getUpdateHistory } from "./api";

describe("features/platform/reliability > api > getUpdateHistory", () => {
  afterEach(() => { vi.restoreAllMocks(); });

  // Regression test for a real bug caught by the E2E suite: GetUpdateHistory
  // (internal/api/handlers/update.go) responds with an envelope,
  // {"history": [...]}, not a bare array — unlike the sibling
  // status/check/preflight endpoints on the same resource. A bare
  // `request<UpdateHistoryRow[]>` cast previously left the envelope
  // object flowing straight into UpdatesPanel's `history.map(...)`,
  // throwing "history.map is not a function" against the real backend
  // even though every unit test mocked the (already-unwrapped) return
  // shape and stayed green.
  it("unwraps the {history: [...]} envelope into a bare array", async () => {
    vi.spyOn(client, "request").mockResolvedValue({
      history: [{ id: 1, startedAt: "2026-01-01T00:00:00Z", durationSeconds: 5, previousSha: "a", newSha: "b", fromVersion: "1.0.0", toVersion: "1.0.1", status: "completed", severity: "info", actor: "psa" }],
    });
    const result = await getUpdateHistory();
    expect(Array.isArray(result)).toBe(true);
    expect(result[0].toVersion).toBe("1.0.1");
  });
});
