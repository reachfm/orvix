import { describe, expect, it } from "vitest";
import { formatMinorUnits, formatSignedMinorUnits } from "./formatters";

describe("platform-billing formatters", () => {
  it("formats minor units with a currency", () => {
    expect(formatMinorUnits(100, "USD")).toContain("1");
    expect(formatMinorUnits(0, "USD")).toContain("0");
  });

  it("falls back to a neutral string when currency is missing", () => {
    expect(formatMinorUnits(42, "")).toContain("42");
    expect(formatMinorUnits(42, undefined)).toContain("42");
  });

  it("formats signed values", () => {
    expect(formatSignedMinorUnits(-100, "USD")).toContain("-");
    expect(formatSignedMinorUnits(100, "USD")).not.toContain("-");
  });
});
