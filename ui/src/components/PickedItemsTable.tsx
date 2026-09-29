import { Link } from "@tanstack/react-router";

import { formatBytes, formatDateTime } from "../format";
import { PickedItem } from "../types/photos";
import Icon from "./ui/Icon";
import Table, { Column } from "./ui/Table";

// Items Google Photos scans picked, for a scan's results and for Browse.
// Sizes are of the copy Google Photos keeps; see
// docs/archive/photos-picker.md, "Step 0 findings".

const sizeNotes: Record<PickedItem["size_source"], string> = {
  head: "The size of the copy Google Photos keeps",
  download: "The size of the copy Google Photos keeps, found by downloading it",
  unavailable: "Google Photos didn't give a size for this item",
};

function camera(item: PickedItem): string {
  const { camera_make: make, camera_model: model } = item;
  // Models often start with the make ("Pixel 8 Pro" from "Google" doesn't).
  return model.startsWith(make) ? model : `${make} ${model}`.trim();
}

/** The table, with a link to the scan that picked each item when given. */
export default function PickedItemsTable<Row extends PickedItem>({
  rows,
  scanOf,
}: {
  rows: Row[];
  scanOf?: (row: Row) => number;
}) {
  const columns: Column<Row>[] = [
    {
      header: "Name",
      primary: true,
      cell: (item) => (
        <span className="flex min-w-0 items-center gap-1.5">
          <Icon
            name={item.media_type === "VIDEO" ? "video" : "image"}
            className="shrink-0 text-muted"
          />
          <span className="wrap-anywhere">{item.filename}</span>
        </span>
      ),
    },
    {
      header: "Taken",
      cell: (item) =>
        item.create_time ? formatDateTime(item.create_time) : "",
      className: "sm:whitespace-nowrap",
    },
    {
      header: "Dimensions",
      cell: (item) =>
        item.width && item.height ? `${item.width} × ${item.height}` : "",
      className: "sm:whitespace-nowrap",
    },
    { header: "Camera", cell: camera, className: "wrap-anywhere" },
    {
      header: "Size",
      numeric: true,
      cell: (item) => (
        <span title={sizeNotes[item.size_source]}>
          {item.size === null ? "Unknown" : `~${formatBytes(item.size)}`}
        </span>
      ),
    },
  ];
  if (scanOf) {
    columns.push({
      header: "Scan",
      numeric: true,
      cell: (row) => (
        <Link
          to="/scans/$scanId"
          params={{ scanId: String(scanOf(row)) }}
          search={{ page: 1 }}
          className="text-accent hover:underline"
        >
          {scanOf(row)}
        </Link>
      ),
    });
  }
  return (
    <Table
      rowKey={(item) => item.media_item_id}
      rows={rows}
      columns={columns}
    />
  );
}
