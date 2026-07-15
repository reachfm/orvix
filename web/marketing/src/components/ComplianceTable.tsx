interface Row {
  topic: string;
  detail: string;
}

interface ComplianceTableProps {
  rows: Row[];
  caption?: string;
}

export default function ComplianceTable({ rows, caption }: ComplianceTableProps) {
  return (
    <div
      style={{
        overflowX: "auto",
        background: "var(--bg-canvas)",
        border: "1px solid var(--border-default)",
        borderRadius: "var(--r-lg)",
      }}
    >
      <table
        style={{
          width: "100%",
          borderCollapse: "collapse",
          fontSize: "var(--fs-sm)",
        }}
      >
        {caption && (
          <caption
            style={{
              captionSide: "top",
              textAlign: "left",
              padding: "var(--sp-4) var(--sp-5)",
              color: "var(--text-faint)",
              fontSize: "var(--fs-xs)",
            }}
          >
            {caption}
          </caption>
        )}
        <thead>
          <tr>
            <th
              scope="col"
              style={{
                textAlign: "left",
                padding: "var(--sp-4) var(--sp-5)",
                borderBottom: "1px solid var(--border-default)",
                color: "var(--text-secondary)",
                fontWeight: 600,
                width: "32%",
              }}
            >
              Topic
            </th>
            <th
              scope="col"
              style={{
                textAlign: "left",
                padding: "var(--sp-4) var(--sp-5)",
                borderBottom: "1px solid var(--border-default)",
                color: "var(--text-secondary)",
                fontWeight: 600,
              }}
            >
              How Orvix handles it
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row, idx) => (
            <tr key={idx}>
              <th
                scope="row"
                style={{
                  textAlign: "left",
                  padding: "var(--sp-4) var(--sp-5)",
                  borderBottom: "1px solid var(--border-subtle)",
                  color: "var(--text-primary)",
                  fontWeight: 500,
                  verticalAlign: "top",
                }}
              >
                {row.topic}
              </th>
              <td
                style={{
                  textAlign: "left",
                  padding: "var(--sp-4) var(--sp-5)",
                  borderBottom: "1px solid var(--border-subtle)",
                  color: "var(--text-secondary)",
                  lineHeight: 1.6,
                  verticalAlign: "top",
                }}
              >
                {row.detail}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
