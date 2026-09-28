import { ReactNode } from "react";

export type Column<Row> = {
  header: string;
  cell: (row: Row) => ReactNode;
  // Numbers: right-aligned, in tabular figures.
  numeric?: boolean;
  // The row's title on phones, shown without its header.
  primary?: boolean;
  className?: string;
};

/**
 * A compact table. Below sm each row becomes a stacked card: the primary
 * cell as its title, then "header: value" lines. One markup for both, so
 * it's read the same everywhere.
 */
export default function Table<Row>({
  columns,
  rows,
  rowKey,
}: {
  columns: Column<Row>[];
  rows: Row[];
  rowKey: (row: Row) => string | number;
}) {
  return (
    <table className="block w-full text-sm sm:table">
      <thead className="hidden border-b border-line bg-surface-muted/60 text-xs text-muted sm:table-header-group">
        <tr>
          {columns.map((c) => (
            <th
              key={c.header}
              scope="col"
              className={`px-3 py-2 font-medium ${c.numeric ? "text-right" : "text-left"}`}
            >
              {c.header}
            </th>
          ))}
        </tr>
      </thead>
      <tbody className="block space-y-2 sm:table-row-group sm:space-y-0">
        {rows.map((row) => (
          <tr
            key={rowKey(row)}
            className="block rounded-lg border border-line p-3 sm:table-row sm:rounded-none sm:border-0 sm:border-b sm:p-0 sm:last:border-b-0 sm:hover:bg-surface-muted/40"
          >
            {columns.map((c) => (
              <td
                key={c.header}
                data-label={c.header}
                className={`sm:table-cell sm:px-3 sm:py-2 sm:align-top ${
                  c.primary
                    ? "block pb-1 font-medium sm:font-normal"
                    : "flex justify-between gap-4 py-0.5 before:shrink-0 before:text-muted before:content-[attr(data-label)] sm:before:content-none"
                } ${c.numeric ? "tabular-nums sm:text-right sm:whitespace-nowrap" : ""} ${c.className ?? ""}`}
              >
                {c.cell(row)}
              </td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  );
}
