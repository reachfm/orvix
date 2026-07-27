// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, fireEvent } from "@testing-library/react";
import { afterEach, describe, it, expect, vi } from "vitest";
import DataTable from "../DataTable";

const columns = [
  { key: "name", label: "Name" },
  { key: "role", label: "Role" },
];

const rows = [
  { id: 1, name: "Alice", role: "Admin" },
  { id: 2, name: "Bob", role: "User" },
];

describe("DataTable", () => {
  afterEach(cleanup);

  it("renders column headers", () => {
    render(<DataTable columns={columns} rows={rows} />);
    expect(screen.getByText("Name")).toBeInTheDocument();
    expect(screen.getByText("Role")).toBeInTheDocument();
  });

  it("renders row data", () => {
    render(<DataTable columns={columns} rows={rows} />);
    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(screen.getByText("Bob")).toBeInTheDocument();
  });

  it("shows skeleton when loading", () => {
    const { container } = render(<DataTable columns={columns} rows={[]} loading />);
    expect(container.querySelectorAll(".orvix-skeleton").length).toBeGreaterThan(0);
  });

  it("shows emptyState when no rows", () => {
    render(<DataTable columns={columns} rows={[]} emptyState={<p>Nothing here</p>} />);
    expect(screen.getByText("Nothing here")).toBeInTheDocument();
  });

  it("calls onRowClick with row data", () => {
    const onRowClick = vi.fn();
    render(<DataTable columns={columns} rows={rows} onRowClick={onRowClick} />);
    fireEvent.click(screen.getByText("Alice"));
    expect(onRowClick).toHaveBeenCalledWith(rows[0]);
  });
});
