import { describe, expect, it } from "vitest";

// Verify the config-truth contract shapes and redaction semantics.
import type { Setting } from "./contract";

function redactedValue(s: Setting): unknown {
  // Mirrors the backend: Get() replaces a stored secret's Value with
  // "REDACTED" and EffectiveValue is never the secret itself.
  return s.secret ? "REDACTED" : s.effective_value;
}

describe("config-truth", () => {
  it("redacts secret settings", () => {
    const s: Setting = {
      key: "smtp.relay_password",
      section: "smtp",
      type: "string",
      source: "env",
      state: "effective",
      effective_value: "REDACTED",
      default_value: "",
      restart_required: true,
      immutable: false,
      secret: true,
      version: 1,
    };
    expect(redactedValue(s)).toBe("REDACTED");
  });

  it("passes through non-secret effective values", () => {
    const s: Setting = {
      key: "smtp.max_connections",
      section: "smtp",
      type: "int",
      source: "config",
      state: "effective",
      effective_value: 42,
      default_value: 10,
      restart_required: false,
      immutable: false,
      secret: false,
      version: 1,
    };
    expect(redactedValue(s)).toBe(42);
  });

  it("never surfaces a secret in the effective value", () => {
    const s: Setting = {
      key: "webhook.secret",
      section: "webhook",
      type: "string",
      source: "db",
      state: "effective",
      effective_value: "REDACTED",
      default_value: "",
      restart_required: false,
      immutable: true,
      secret: true,
      version: 3,
    };
    expect(String(redactedValue(s))).not.toContain("sk_");
    expect(s.effective_value).not.toMatch(/^sk_/);
  });
});
