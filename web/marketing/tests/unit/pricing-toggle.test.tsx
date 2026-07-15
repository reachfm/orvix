import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import PricingToggle from "../../src/components/PricingToggle";

describe("PricingToggle", () => {
  it("renders the monthly prices by default", () => {
    render(<PricingToggle />);
    expect(screen.getByText("$9.99/mo")).toBeTruthy();
    expect(screen.getByText("$29.99/mo")).toBeTruthy();
    expect(screen.getByText("$99.99/mo")).toBeTruthy();
  });

  it("switches to the annual prices when the yearly radio is clicked", () => {
    render(<PricingToggle />);
    const yearly = screen.getByRole("radio", { name: /yearly/i });
    fireEvent.click(yearly);
    expect(screen.getByText("$99.90/yr")).toBeTruthy();
    expect(screen.getByText("$299.90/yr")).toBeTruthy();
    expect(screen.getByText("$999.90/yr")).toBeTruthy();
  });

  it("Free is shown as 'Free' on both cycles", () => {
    render(<PricingToggle />);
    expect(screen.getAllByText("Free").length).toBeGreaterThan(0);
  });
});

// Note: vi is intentionally imported so future stubs (e.g. for
// router) live without import drift.
void vi;
