// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, fireEvent } from "@testing-library/react";
import { afterEach, describe, it, expect, vi } from "vitest";
import Dialog from "../Dialog";

describe("Dialog", () => {
  afterEach(cleanup);

  it("renders when open=true", () => {
    render(<Dialog open title="Test Dialog" onClose={() => {}}><p>Content</p></Dialog>);
    expect(screen.getByText("Test Dialog")).toBeInTheDocument();
    expect(screen.getByText("Content")).toBeInTheDocument();
  });

  it("does not render when open=false", () => {
    render(<Dialog open={false} title="Hidden" onClose={() => {}} />);
    expect(screen.queryByText("Hidden")).toBeNull();
  });

  it("has aria-modal attribute", () => {
    render(<Dialog open title="Modal" onClose={() => {}} />);
    expect(screen.getByRole("dialog")).toHaveAttribute("aria-modal", "true");
  });

  it("calls onClose on Escape key", () => {
    const onClose = vi.fn();
    render(<Dialog open title="Esc" onClose={onClose} />);
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("displays description when provided", () => {
    render(<Dialog open title="Test" description="Description text" onClose={() => {}} />);
    expect(screen.getByText("Description text")).toBeInTheDocument();
  });
});
