import { createFileRoute, Link } from "@tanstack/react-router";
import { keepPreviousData, useQuery } from "@tanstack/react-query";

import {
  getGmailData,
  getScanData,
  getScannedAccounts,
  getScanSummary,
} from "../api";
import { accountLabels } from "../accountLabels";
import Breadcrumbs from "../components/Breadcrumbs";
import Masked, { MaskToggle } from "../components/Masked";
import { queryKeys } from "../api/queryKeys";
import { formatBytes, formatDateTime, formatDuration, scanTypeLabel } from "../format";
import { Table, Td, Tr } from "../components/Table";
import { MessageRow, ScanDataRow, ScanSummary } from "../types/results";

// A scan's results: its summary, then a page of what it found. See
// docs/specs/request-drive-scans.md, "Results view".

type ResultsSearch = { page: number };

export const Route = createFileRoute("/scans/$scanId")({
  component: ScanResults,
  validateSearch: (search: Record<string, unknown>): ResultsSearch => {
    const page = Number(search.page);
    return { page: Number.isInteger(page) && page > 0 ? page : 1 };
  },
});

// Rows per page; the backend's page size.
const PAGE_SIZE = 10;

function ScanResults() {
  const scanId = Number(Route.useParams().scanId);
  const { page } = Route.useSearch();
  const validId = Number.isInteger(scanId) && scanId > 0;

  const { data: summary, error } = useQuery({
    queryKey: queryKeys.scanSummary(scanId),
    queryFn: () => getScanSummary(scanId),
    enabled: validId,
    // A running scan's totals change; poll until it ends.
    refetchInterval: (query) =>
      query.state.data?.status === "Running" ? 10_000 : false,
  });

  return (
    <>
      <ScanTrail scanId={scanId} summary={summary} />
      {!validId ? (
        <p className="p-3 text-red-500">No such scan.</p>
      ) : error ? (
        <p className="p-3 text-red-500">
          Couldn't load scan {scanId}: {error.message}
        </p>
      ) : !summary ? (
        <p className="p-3">Loading scan {scanId}…</p>
      ) : (
        <ScanDetails summary={summary} scanId={scanId} page={page} />
      )}
    </>
  );
}

// Request History › <account> › Scan N: where a scan lives, however it was
// opened. The account link goes back to that account's scans.
function ScanTrail({
  scanId,
  summary,
}: {
  scanId: number;
  summary: ScanSummary | undefined;
}) {
  const { data: scannedAccounts } = useQuery({
    queryKey: queryKeys.scannedAccounts,
    queryFn: () => getScannedAccounts(),
    staleTime: Infinity,
  });
  const clientKey = summary?.client_key;
  const items = [
    <Link to="/requests" search={{}} className="underline">
      Request History
    </Link>,
  ];
  if (clientKey) {
    const label =
      accountLabels(scannedAccounts ?? []).get(clientKey) ?? summary.name;
    items.push(
      <Link to="/requests" search={{ account: clientKey }} className="underline">
        {label}
      </Link>
    );
  }
  items.push(<>Scan {scanId}</>);
  return <Breadcrumbs items={items} />;
}

function ScanDetails({
  summary,
  scanId,
  page,
}: {
  summary: ScanSummary;
  scanId: number;
  page: number;
}) {
  return (
    <div className="p-2">
      <h2 className="p-2 font-bold text-xl">
        Scan {summary.scan_id}: {scanTypeLabel(summary.scan_type)}
      </h2>
      <Summary summary={summary} />
      {summary.scan_type === "gmail" ? (
        <Messages scanId={scanId} page={page} />
      ) : summary.scan_type === "google_drive" || summary.scan_type === "local" ? (
        <Files scanId={scanId} page={page} />
      ) : (
        <p className="p-3">No results view for this scan type yet.</p>
      )}
    </div>
  );
}

// "1 file", "2 files".
function count(n: number, noun: string): string {
  return `${n} ${noun}${n === 1 ? "" : "s"}`;
}

function Summary({ summary }: { summary: ScanSummary }) {
  const seconds = Number(summary.scan_duration_in_sec);
  const isGmail = summary.scan_type === "gmail";
  // Per review item 7.9, a Gmail scan saves only the messages new in it.
  const items = isGmail
    ? count(summary.item_count, "new message")
    : count(summary.item_count, "file");
  const folders =
    summary.folder_count > 0 ? `, ${count(summary.folder_count, "folder")}` : "";
  const rows: [string, string][] = [
    ["Account", summary.name],
    ["Folder", summary.search_path],
    [isGmail ? "Filter" : "Query", summary.search_filter],
    ["Status", summary.status],
    ["Started", formatDateTime(summary.scan_start_time)],
    ["Duration", seconds < 0 ? "Not finished" : formatDuration(seconds)],
    ["Found", `${items}, ${formatBytes(summary.total_bytes)}${folders}`],
  ];
  // Browse shows what all the account's scans of the service found.
  const service =
    summary.scan_type === "gmail" ? "gmail" : summary.scan_type === "google_drive" ? "drive" : null;
  return (
    <>
      <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 p-2">
        {rows
          .filter(([, value]) => value !== "")
          .map(([label, value]) => (
            <div key={label} className="contents">
              <dt className="font-semibold">{label}</dt>
              <dd className="wrap-anywhere">{value}</dd>
            </div>
          ))}
      </dl>
      {service && summary.client_key && (
        <p className="p-2">
          <Link
            to="/"
            search={{ source: `google:${summary.client_key}`, service }}
            className="underline"
          >
            Browse this account's {service === "gmail" ? "Gmail" : "Google Drive"}
          </Link>
        </p>
      )}
    </>
  );
}

function Pager({ page, total }: { page: number; total: number }) {
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  return (
    <nav className="flex items-center gap-4 p-2" aria-label="Pages">
      {page > 1 ? (
        <Link from={Route.fullPath} search={{ page: page - 1 }} className="underline">
          Previous
        </Link>
      ) : (
        <span className="text-gray-400">Previous</span>
      )}
      <span>
        Page {page} of {pages}
      </span>
      {page < pages ? (
        <Link from={Route.fullPath} search={{ page: page + 1 }} className="underline">
          Next
        </Link>
      ) : (
        <span className="text-gray-400">Next</span>
      )}
    </nav>
  );
}

// The folder part of a path: the path without the row's own name.
function folderOf(row: ScanDataRow): string {
  const suffix = "/" + row.name;
  if (row.path.endsWith(suffix)) {
    return row.path.slice(0, -suffix.length);
  }
  return row.path.slice(0, Math.max(0, row.path.lastIndexOf("/")));
}

// Where a Drive file or folder opens.
function driveUrl(row: ScanDataRow): string | null {
  if (!row.file_id) {
    return null;
  }
  const id = encodeURIComponent(row.file_id);
  return row.is_dir
    ? `https://drive.google.com/drive/folders/${id}`
    : `https://drive.google.com/file/d/${id}/view`;
}

function Files({ scanId, page }: { scanId: number; page: number }) {
  const { data, error } = useQuery({
    queryKey: queryKeys.scanResults(scanId, page),
    queryFn: () => getScanData(scanId, page),
    placeholderData: keepPreviousData,
  });
  if (error) {
    return <p className="p-3 text-red-500">Couldn't load results: {error.message}</p>;
  }
  if (!data) {
    return <p className="p-3">Loading results…</p>;
  }
  if (data.pagination_info.size === 0) {
    return <p className="p-3">This scan found no files.</p>;
  }
  return (
    <>
      <Table
        className="w-full"
        headers={["Folder", "Name", "Size", "Files", "Modified", "MD5"]}
      >
        {data.scan_data.map((row) => {
          const url = driveUrl(row);
          const name = row.is_dir ? `${row.name}/` : row.name;
          return (
            <Tr key={row.scan_data_id}>
              <Td className="wrap-anywhere">{folderOf(row)}</Td>
              <Td className="wrap-anywhere">
                {url ? (
                  <a href={url} target="_blank" rel="noreferrer" className="underline">
                    {name}
                  </a>
                ) : (
                  name
                )}
              </Td>
              <Td>{formatBytes(row.size)}</Td>
              <Td>{row.is_dir ? row.file_count : ""}</Td>
              <Td>{row.modified ? formatDateTime(row.modified) : ""}</Td>
              <Td title={row.md5}>{row.md5.slice(0, 8)}</Td>
            </Tr>
          );
        })}
      </Table>
      <Pager page={page} total={data.pagination_info.size} />
    </>
  );
}

function Messages({ scanId, page }: { scanId: number; page: number }) {
  const { data, error } = useQuery({
    queryKey: queryKeys.scanResults(scanId, page),
    queryFn: () => getGmailData(scanId, page),
    placeholderData: keepPreviousData,
  });
  if (error) {
    return <p className="p-3 text-red-500">Couldn't load results: {error.message}</p>;
  }
  if (!data) {
    return <p className="p-3">Loading results…</p>;
  }
  if (data.pagination_info.size === 0) {
    return <p className="p-3">This scan found no new messages.</p>;
  }
  return (
    <>
      <p className="px-2 pt-2">
        <MaskToggle />
      </p>
      <Table className="w-full" headers={["From", "Subject", "Date", "Size"]}>
        {data.message_metadata.map((m: MessageRow) => (
          <Tr key={m.message_metadata_id}>
            <Td className="wrap-anywhere">
              <Masked text={m.from} />
            </Td>
            <Td className="wrap-anywhere">
              <Masked text={m.subject} />
            </Td>
            <Td>{m.date ? formatDateTime(m.date) : ""}</Td>
            <Td>{formatBytes(m.size_estimate)}</Td>
          </Tr>
        ))}
      </Table>
      <Pager page={page} total={data.pagination_info.size} />
    </>
  );
}
