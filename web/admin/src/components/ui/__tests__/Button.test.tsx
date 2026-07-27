// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, fireEvent } from "@testing-library/react";
import { afterEach, describe, it, expect, vi } from "vitest";
import Button from "../Button";

describe("Button", () => {
  afterEach(cleanup);

  it("renders children", () => {
    render(<Button>Click me</Button>);
    expect(screen.getByText("Click me")).toBeInTheDocument();
  });

  it("renders each variant", () => {
    render(<><Button variant="primary">Primary</Button><Button variant="secondary">Secondary</Button><Button variant="ghost">Ghost</Button><Button variant="danger">Danger</Button></>);
    expect(screen.getByText("Primary")).toBeInTheDocument();
    expect(screen.getByText("Secondary")).toBeInTheDocument();
    expect(screen.getByText("Ghost")).toBeInTheDocument();
    expect(screen.getByText("Danger")).toBeInTheDocument();
  });

  it("disabled prevents onClick", () => {
    const onClick = vi.fn();
    render(<Button disabled onClick={onClick}>Disabled</Button>);
    fireEvent.click(screen.getByText("Disabled"));
    expect(onClick).not.toHaveBeenCalled();
  });

  it("loading shows spinner and disables", () => {
    const onClick = vi.fn();
    render(<Button loading onClick={onClick}>Loading</Button>);
    expect(document.querySelector(".orvix-btn-spinner")).toBeTruthy();
    fireEvent.click(screen.getByText("Loading"));
    expect(onClick).not.toHaveBeenCalled();
  });

  it("applies fullWidth class", () => {
    const { container } = render(<Button fullWidth>Full</Button>);
    expect(container.querySelector(".orvix-btn-full")).toBeTruthy();
  });
});
