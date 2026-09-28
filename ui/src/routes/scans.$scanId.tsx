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
import {
  formatBytes,
  formatCount,
  formatDateTime,
  formatDuration,
  scanTypeLabel,
} from "../format";
import ScanStatus from "../components/ScanStatus";
import Card from "../components/ui/Card";
import Icon from "../components/ui/Icon";
import Pager from "../components/ui/Pager";
import Table from "../components/ui/Table";
import { buttonClasses } from "../components/ui/styles";
import { fileKind } from "../fileTypes";
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
        <p className="py-6 text-sm text-danger">No such scan.</p>
      ) : error ? (
        <p className="py-6 text-sm text-danger">
          Couldn't load scan {scanId}: {error.message}
        </p>
      ) : !summary ? (
        <p className="py-6 text-sm text-muted">Loading scan {scanId}…</p>
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
    <Link to="/requests" search={{}}>
      Request History
    </Link>,
  ];
  if (clientKey) {
    const label =
      accountLabels(scannedAccounts ?? []).get(clientKey) ?? summary.name;
    items.push(
      <Link to="/requests" search={{ account: clientKey }}>
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
    <div className="space-y-4">
      <Summary summary={summary} />
      {summary.scan_type === "gmail" ? (
        <Messages scanId={scanId} page={page} />
      ) : summary.scan_type === "google_drive" ||
        summary.scan_type === "local" ? (
        <Files scanId={scanId} page={page} />
      ) : (
        <Card>
          <p className="text-sm text-muted">
            No results view for this scan type yet.
          </p>
        </Card>
      )}
    </div>
  );
}

// "1 file", "2 files".
function count(n: number, noun: string): string {
  return `${formatCount(n)} ${noun}${n === 1 ? "" : "s"}`;
}

function Summary({ summary }: { summary: ScanSummary }) {
  const seconds = Number(summary.scan_duration_in_sec);
  const isGmail = summary.scan_type === "gmail";
  // Per review item 7.9, a Gmail scan saves only the messages new in it.
  const items = isGmail
    ? count(summary.item_count, "new message")
    : count(summary.item_count, "file");
  const folders =
    summary.folder_count > 0
      ? `, ${count(summary.folder_count, "folder")}`
      : "";
  const rows: [string, string][] = [
    ["Account", summary.name],
    ["Folder", summary.search_path],
    [isGmail ? "Filter" : "Query", summary.search_filter],
    ["Started", formatDateTime(summary.scan_start_time)],
    ["Duration", seconds < 0 ? "Not finished" : formatDuration(seconds)],
    ["Found", `${items}, ${formatBytes(summary.total_bytes)}${folders}`],
  ];
  // Browse shows what all the account's scans of the service found.
  const service =
    summary.scan_type === "gmail"
      ? "gmail"
      : summary.scan_type === "google_drive"
        ? "drive"
        : null;
  return (
    <Card
      title={
        <span className="text-xl">
          Scan {summary.scan_id} · {scanTypeLabel(summary.scan_type)}
        </span>
      }
      actions={
        <>
          <ScanStatus status={summary.status} />
          {service && summary.client_key && (
            <Link
              to="/"
              search={{ source: `google:${summary.client_key}`, service }}
              className={buttonClasses("secondary", "sm")}
            >
              Browse this account's{" "}
              {service === "gmail" ? "Gmail" : "Google Drive"}
            </Link>
          )}
        </>
      }
    >
      <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[max-content_1fr]">
        {rows
          .filter(([, value]) => value !== "")
          .map(([label, value]) => (
            <div key={label} className="contents">
              <dt className="text-muted">{label}</dt>
              <dd
                className={`wrap-anywhere ${label === "Query" || label === "Filter" ? "font-mono text-xs" : ""}`}
              >
                {value}
              </dd>
            </div>
          ))}
      </dl>
    </Card>
  );
}

// Page links for a table of results, above it.
function ResultsPager({ page, total }: { page: number; total: number }) {
  return (
    <Pager
      page={page}
      pages={Math.max(1, Math.ceil(total / PAGE_SIZE))}
      link={(to, children, className) => (
        <Link from={Route.fullPath} search={{ page: to }} className={className}>
          {children}
        </Link>
      )}
    />
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
  let body;
  if (error) {
    body = (
      <p className="text-sm text-danger">
        Couldn't load results: {error.message}
      </p>
    );
  } else if (!data) {
    body = <p className="text-sm text-muted">Loading results…</p>;
  } else if (data.pagination_info.size === 0) {
    body = <p className="text-sm text-muted">This scan found no files.</p>;
  } else {
    body = (
      <>
        <ResultsPager page={page} total={data.pagination_info.size} />
        <Table
          rowKey={(row) => row.scan_data_id}
          rows={data.scan_data}
          columns={[
            {
              header: "Name",
              primary: true,
              cell: (row) => {
                const url = driveUrl(row);
                const name = row.is_dir ? `${row.name}/` : row.name;
                return (
                  <span className="flex min-w-0 items-center gap-1.5">
                    <Icon
                      name={row.is_dir ? "folder" : fileKind(row.name)}
                      className={`shrink-0 ${row.is_dir ? "text-accent" : "text-muted"}`}
                    />
                    {url ? (
                      <a
                        href={url}
                        target="_blank"
                        rel="noreferrer"
                        className="wrap-anywhere hover:underline"
                      >
                        {name}
                      </a>
                    ) : (
                      <span className="wrap-anywhere">{name}</span>
                    )}
                  </span>
                );
              },
            },
            {
              header: "Folder",
              cell: (row) => folderOf(row),
              className: "wrap-anywhere text-muted sm:max-w-xs",
            },
            {
              header: "Size",
              numeric: true,
              cell: (row) => formatBytes(row.size),
            },
            {
              header: "Files",
              numeric: true,
              cell: (row) => (row.is_dir ? formatCount(row.file_count) : ""),
            },
            {
              header: "Modified",
              cell: (row) => (row.modified ? formatDateTime(row.modified) : ""),
              className: "sm:whitespace-nowrap",
            },
            {
              header: "MD5",
              cell: (row) => (
                <span title={row.md5} className="font-mono text-xs">
                  {row.md5.slice(0, 8)}
                </span>
              ),
            },
          ]}
        />
      </>
    );
  }
  return <Card title="Files and folders">{body}</Card>;
}

function Messages({ scanId, page }: { scanId: number; page: number }) {
  const { data, error } = useQuery({
    queryKey: queryKeys.scanResults(scanId, page),
    queryFn: () => getGmailData(scanId, page),
    placeholderData: keepPreviousData,
  });
  let body;
  if (error) {
    body = (
      <p className="text-sm text-danger">
        Couldn't load results: {error.message}
      </p>
    );
  } else if (!data) {
    body = <p className="text-sm text-muted">Loading results…</p>;
  } else if (data.pagination_info.size === 0) {
    body = (
      <p className="text-sm text-muted">This scan found no new messages.</p>
    );
  } else {
    body = (
      <>
        <ResultsPager page={page} total={data.pagination_info.size} />
        <Table
          rowKey={(m: MessageRow) => m.message_metadata_id}
          rows={data.message_metadata}
          columns={[
            {
              header: "Subject",
              primary: true,
              cell: (m) => <Masked text={m.subject} />,
              className: "wrap-anywhere",
            },
            {
              header: "From",
              cell: (m) => <Masked text={m.from} />,
              className: "wrap-anywhere",
            },
            {
              header: "Date",
              cell: (m) => (m.date ? formatDateTime(m.date) : ""),
              className: "sm:whitespace-nowrap",
            },
            {
              header: "Size",
              numeric: true,
              cell: (m) => formatBytes(m.size_estimate),
            },
          ]}
        />
      </>
    );
  }
  return (
    <Card title="New messages" actions={<MaskToggle />}>
      {body}
    </Card>
  );
}
