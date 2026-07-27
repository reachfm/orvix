import Skeleton from "./Skeleton";
import EmptyState from "./EmptyState";
import type { TableColumn } from "../../types/ui";

interface DataTableProps<T> {
  columns: TableColumn<T>[];
  rows: T[];
  loading?: boolean;
  emptyState?: React.ReactNode;
  onRowClick?: (row: T) => void;
  sortable?: boolean;
}

export default function DataTable<T extends Record<string, any>>({ columns, rows, loading, emptyState, onRowClick, sortable }: DataTableProps<T>) {
  if (loading) {
    return (
      <div className="orvix-surface-card overflow-hidden">
        <table className="orvix-table">
          <thead><tr>{columns.map((col) => <th key={col.key} style={col.width ? { width: col.width } : undefined}>{col.label}</th>)}</tr></thead>
          <tbody>
            {[1, 2, 3, 4, 5].map((i) => (
              <tr key={i}>
                {columns.map((col) => (
                  <td key={col.key}><Skeleton height={16} width="60%" /></td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  }

  if (!rows || rows.length === 0) {
    return <>{emptyState || <EmptyState title="No data" />}</>;
  }

  return (
    <div className="orvix-surface-card overflow-hidden">
      <table className="orvix-table" role="table">
        <thead>
          <tr>
            {columns.map((col) => (
              <th key={col.key} scope="col" style={col.width ? { width: col.width } : undefined}>
                {col.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <tr
              key={row.id ?? i}
              onClick={() => onRowClick?.(row)}
              className={onRowClick ? "cursor-pointer" : ""}
            >
              {columns.map((col) => (
                <td key={col.key}>{col.render ? col.render(row) : row[col.key]}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
