import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { ReactNode, useEffect } from "react";

import {
  getAccountMessages,
  getAgentDriveStatus,
  getBrowseChildren,
  getBrowseSources,
} from "../api";
import { queryKeys } from "../api/queryKeys";
import { accountLabels } from "../accountLabels";
import Breadcrumbs from "../components/Breadcrumbs";
import Masked, { MaskToggle } from "../components/Masked";
import FolderTree from "../components/FolderTree";
import { Table, Td, Tr } from "../components/Table";
import { formatAgo, formatBytes, formatCount, formatDateTime } from "../format";
import { BrowseSource, ServiceTotals } from "../types/browse";

// Browse: what you have, by Google account and by agent drive. See
// docs/specs/browse.md, "Browse page".

type BrowseService = "drive" | "gmail";

type BrowseSearch = {
  // "google:<client key>" or "agent:<drive ID>".
  source?: string;
  service?: BrowseService;
  // A Drive folder ID, or an agent folder's path; the root when missing.
  folder?: string;
  // Gmail's order and page.
  sort?: "size" | "date";
  page?: number;
};

export const Route = createFileRoute("/")({
  component: Browse,
  validateSearch: (search: Record<string, unknown>): BrowseSearch => {
    const page = Number(search.page);
    return {
      ...(typeof search.source === "string" && search.source !== ""
        ? { source: search.source }
        : {}),
      ...(search.service === "drive" || search.service === "gmail"
        ? { service: search.service }
        : {}),
      ...(typeof search.folder === "string" && search.folder !== ""
        ? { folder: search.folder }
        : {}),
      ...(search.sort === "size" || search.sort === "date"
        ? { sort: search.sort }
        : {}),
      ...(Number.isInteger(page) && page > 1 ? { page } : {}),
    };
  },
});

// The last source, and each account's last service, kept per browser. Storage
// can be unavailable (private windows, blocked site data); Browse works
// without it.
const lastSourceKey = "browse.source";
const lastServiceKey = (clientKey: string) => `browse.service.${clientKey}`;

function recall(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function remember(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Not kept; that's fine.
  }
}

const sourceId = (s: BrowseSource) => `${s.kind}:${s.key}`;

const serviceNames: Record<BrowseService, string> = {
  drive: "Google Drive",
  gmail: "Gmail",
};

function Browse() {
  const search = Route.useSearch();
  const { data: sources, error } = useQuery({
    queryKey: queryKeys.browseSources,
    queryFn: getBrowseSources,
    // Totals being rebuilt show "updating totals…" until they're done.
    refetchInterval: (query) =>
      query.state.data?.some((s) => s.updating || s.services?.drive.updating)
        ? 30_000
        : false,
  });

  if (error) {
    return (
      <p className="p-3 text-red-500">
        Couldn't load what you can browse: {error.message}
      </p>
    );
  }
  if (!sources) {
    return <p className="p-3">Loading…</p>;
  }
  if (sources.length === 0) {
    return (
      <div className="p-3 space-y-2">
        <p>Nothing to browse yet.</p>
        <p>
          <Link to="/request" search={{}} className="underline">
            Link a Google account
          </Link>{" "}
          on the Request page, or upload a drive's scans with{" "}
          <code>driveagent</code>.
        </p>
      </div>
    );
  }
  // The source in the URL, else the last one used, else the first.
  const source =
    sources.find((s) => sourceId(s) === search.source) ??
    sources.find((s) => sourceId(s) === recall(lastSourceKey)) ??
    sources[0];
  return (
    <SourceView
      key={sourceId(source)}
      sources={sources}
      source={source}
      search={search}
    />
  );
}

function SourceView({
  sources,
  source,
  search,
}: {
  sources: BrowseSource[];
  source: BrowseSource;
  search: BrowseSearch;
}) {
  const navigate = useNavigate({ from: Route.fullPath });
  const id = sourceId(source);
  const service: BrowseService =
    search.service ??
    (recall(lastServiceKey(source.key)) === "gmail" ? "gmail" : "drive");

  useEffect(() => {
    remember(lastSourceKey, id);
    if (source.kind === "google") {
      remember(lastServiceKey(source.key), service);
    }
  }, [id, source.kind, source.key, service]);

  const labels = accountLabels(
    sources
      .filter((s) => s.kind === "google")
      .map((s) => ({ clientKey: s.key, displayName: s.name }))
  );
  const label = (s: BrowseSource) =>
    s.kind === "google" ? (labels.get(s.key) ?? s.name) : s.name;
  const folder = search.folder ?? "";

  return (
    <>
      <Trail
        source={source}
        label={label(source)}
        service={service}
        folder={folder}
      />
      <div className="p-2 space-y-3">
        <label className="flex items-center gap-2">
          <span className="font-semibold">Source</span>
          <select
            value={id}
            onChange={(e) => navigate({ search: { source: e.target.value } })}
            className="border rounded p-1 dark:bg-gray-800"
          >
            <optgroup label="Google accounts">
              {sources
                .filter((s) => s.kind === "google")
                .map((s) => (
                  <option key={sourceId(s)} value={sourceId(s)}>
                    {label(s)}
                  </option>
                ))}
            </optgroup>
            <optgroup label="Drives">
              {sources
                .filter((s) => s.kind === "agent")
                .map((s) => (
                  <option key={sourceId(s)} value={sourceId(s)}>
                    {label(s)}
                  </option>
                ))}
            </optgroup>
          </select>
        </label>
        {source.kind === "google" ? (
          <GoogleAccount
            source={source}
            service={service}
            folder={folder}
            search={search}
          />
        ) : (
          <AgentDrive source={source} folder={folder} />
        )}
      </div>
    </>
  );
}

// Browse › <source> › <service> › <folder path>, each part a link back up.
function Trail({
  source,
  label,
  service,
  folder,
}: {
  source: BrowseSource;
  label: string;
  service: BrowseService;
  folder: string;
}) {
  const id = sourceId(source);
  const showsFolders = source.kind === "agent" || service === "drive";
  // The folder's path; the tree's first page, already loaded.
  const { data } = useQuery({
    queryKey: queryKeys.browseChildren(id, folder, 1),
    queryFn: () => getBrowseChildren(source.kind, source.key, folder, 1),
    enabled: showsFolders && folder !== "",
  });
  const path = folder !== "" ? (data?.path ?? []) : [];
  const serviceSearch = source.kind === "google" ? { service } : {};

  const items: ReactNode[] = [
    <Link to="/" search={{}} className="underline">
      Browse
    </Link>,
    <Link to="/" search={{ source: id }} className="underline">
      {label}
    </Link>,
  ];
  if (source.kind === "google") {
    items.push(
      <Link to="/" search={{ source: id, service }} className="underline">
        {serviceNames[service]}
      </Link>
    );
  }
  for (const part of path) {
    items.push(
      <Link
        to="/"
        search={{ source: id, ...serviceSearch, folder: part.id }}
        className="underline"
      >
        {part.name}
      </Link>
    );
  }
  // The last part is the page itself.
  const last = items.length - 1;
  items[last] =
    path.length > 0
      ? path[path.length - 1].name
      : source.kind === "google"
        ? serviceNames[service]
        : label;
  return <Breadcrumbs items={items} />;
}

// "504 files · 3.7 GB"
function totals(files: number, bytes: number, noun = "file"): string {
  return `${formatCount(files)} ${noun}${files === 1 ? "" : "s"} · ${formatBytes(bytes)}`;
}

function Updating() {
  return <span className="ml-2 text-sm text-gray-500">updating totals…</span>;
}

function GoogleAccount({
  source,
  service,
  folder,
  search,
}: {
  source: BrowseSource;
  service: BrowseService;
  folder: string;
  search: BrowseSearch;
}) {
  const navigate = useNavigate({ from: Route.fullPath });
  const services = source.services!;
  const tab = (name: BrowseService, st: ServiceTotals) => {
    const selected = name === service;
    return (
      <button
        key={name}
        type="button"
        role="tab"
        aria-selected={selected}
        onClick={() =>
          navigate({ search: { source: sourceId(source), service: name } })
        }
        className={`border rounded px-3 py-1 text-left ${selected ? "font-bold border-gray-700 dark:border-gray-300" : ""}`}
      >
        {serviceNames[name]}
        <span className="block text-sm font-normal text-gray-500">
          {st.files !== undefined && st.bytes !== undefined
            ? totals(st.files, st.bytes, name === "gmail" ? "message" : "file")
            : st.granted
              ? "Not scanned"
              : "Not granted"}
        </span>
      </button>
    );
  };
  const current = services[service];
  return (
    <>
      <div role="tablist" className="flex flex-wrap gap-2">
        {tab("drive", services.drive)}
        {tab("gmail", services.gmail)}
        <button
          type="button"
          role="tab"
          aria-selected={false}
          disabled
          className="border rounded px-3 py-1 text-left text-gray-400"
        >
          Google Photos
          <span className="block text-sm">
            Not available yet (review item 7.15)
          </span>
        </button>
      </div>
      {current.files === undefined ? (
        <NotRecorded
          source={source}
          service={service}
          granted={current.granted}
        />
      ) : service === "drive" ? (
        <>
          <p className="text-sm text-gray-500">
            As of the last scan, {formatAgo(current.updated_at!)}.
            {current.updating && <Updating />}
          </p>
          <FolderTree
            source={{ kind: "google", key: source.key }}
            folder={folder}
            onOpen={(f) =>
              navigate({
                search: { source: sourceId(source), service, folder: f },
              })
            }
          />
        </>
      ) : (
        <Messages
          clientKey={source.key}
          sort={search.sort ?? "size"}
          page={search.page ?? 1}
        />
      )}
    </>
  );
}

// A service with nothing recorded: why, and where to fix it.
function NotRecorded({
  source,
  service,
  granted,
}: {
  source: BrowseSource;
  service: BrowseService;
  granted: boolean;
}) {
  return (
    <p className="p-2">
      {granted
        ? `This account's ${serviceNames[service]} hasn't been scanned yet. `
        : `This account hasn't granted ${serviceNames[service]} access yet. `}
      <Link
        to="/request"
        search={{ type: service, account: source.key }}
        className="underline"
      >
        {granted ? "Scan it" : "Grant access"} on the Request page
      </Link>
      .
    </p>
  );
}

function AgentDrive({
  source,
  folder,
}: {
  source: BrowseSource;
  folder: string;
}) {
  const navigate = useNavigate({ from: Route.fullPath });
  return (
    <>
      <div>
        <p>
          {totals(source.files ?? 0, source.bytes ?? 0)}
          {source.updating && <Updating />}
        </p>
        {source.physical_drive !== undefined && (
          <p className="text-sm text-gray-500">
            Linked copy of physical drive {source.physical_drive}
          </p>
        )}
        <AgentStatus driveKey={source.key} />
      </div>
      <FolderTree
        source={{ kind: "agent", key: source.key }}
        folder={folder}
        onOpen={(f) =>
          navigate({ search: { source: sourceId(source), folder: f } })
        }
      />
    </>
  );
}

// The drive's last scan and sync, collapsed to one line.
function AgentStatus({ driveKey }: { driveKey: string }) {
  const { data: status, error } = useQuery({
    queryKey: queryKeys.agentStatus(driveKey),
    queryFn: () => getAgentDriveStatus(driveKey),
  });
  if (error) {
    return (
      <p className="text-sm text-red-500">
        Couldn't load the drive's status: {error.message}
      </p>
    );
  }
  if (!status) {
    return null;
  }
  const run = status.last_scan;
  const summary = [
    run ? `Last scan ${formatAgo(run.started_at)}` : "Never scanned",
    status.last_synced_at
      ? `synced ${formatAgo(status.last_synced_at)}`
      : "never synced",
  ].join(" · ");
  const outcome = !run
    ? ""
    : run.finished_at === null
      ? run.interrupted
        ? "interrupted"
        : "running or cut off"
      : run.interrupted
        ? "interrupted"
        : "completed";
  return (
    <details className="text-sm">
      <summary className="cursor-pointer text-gray-600 dark:text-gray-300">
        {summary}
      </summary>
      <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 p-2">
        {run && (
          <>
            <dt className="font-semibold">Last scan</dt>
            <dd>
              {formatDateTime(run.started_at)}
              {run.finished_at &&
                ` to ${formatDateTime(run.finished_at)}`}, {outcome}
              {run.files_seen !== null &&
                `, ${formatCount(run.files_seen)} files seen`}
            </dd>
          </>
        )}
        <dt className="font-semibold">Last sync</dt>
        <dd>
          {status.last_synced_at
            ? formatDateTime(status.last_synced_at)
            : "Never"}
        </dd>
        {status.physical_drive !== null && (
          <>
            <dt className="font-semibold">Physical drive</dt>
            <dd>Linked copy of physical drive {status.physical_drive}</dd>
          </>
        )}
      </dl>
    </details>
  );
}

function Messages({
  clientKey,
  sort,
  page,
}: {
  clientKey: string;
  sort: "size" | "date";
  page: number;
}) {
  const { data, error } = useQuery({
    queryKey: queryKeys.accountMessages(clientKey, sort, page),
    queryFn: () => getAccountMessages(clientKey, sort, page),
    placeholderData: keepPreviousData,
  });
  const sortLink = (to: "size" | "date", text: string) =>
    to === sort ? (
      <span className="font-semibold">{text}</span>
    ) : (
      <Link
        from={Route.fullPath}
        search={(prev) => ({ ...prev, sort: to, page: undefined })}
        className="underline"
      >
        {text}
      </Link>
    );
  if (error) {
    return (
      <p className="p-3 text-red-500">
        Couldn't load messages: {error.message}
      </p>
    );
  }
  if (!data) {
    return <p className="p-3">Loading messages…</p>;
  }
  const pages = Math.max(1, Math.ceil(data.total / data.page_size));
  const pageLink = (to: number, text: string) =>
    to >= 1 && to <= pages ? (
      <Link
        from={Route.fullPath}
        search={(prev) => ({ ...prev, page: to > 1 ? to : undefined })}
        className="underline"
      >
        {text}
      </Link>
    ) : (
      <span className="text-gray-400">{text}</span>
    );
  return (
    <div>
      <p className="text-sm text-gray-500">
        Every message this account's Gmail scans found. Messages deleted in
        Gmail since are still listed.
      </p>
      <p className="flex flex-wrap items-center gap-3 pt-2">
        {sortLink("size", "Largest first")} {sortLink("date", "Newest first")}
        <MaskToggle />
      </p>
      <Table
        className="w-full"
        headers={["From", "Subject", "Date", "Size", "Labels", "Scan"]}
      >
        {data.messages.map((m) => (
          <Tr key={m.message_metadata_id}>
            <Td className="wrap-anywhere">
              <Masked text={m.from} />
            </Td>
            <Td className="wrap-anywhere">
              <Masked text={m.subject} />
            </Td>
            <Td>{m.date ? formatDateTime(m.date) : ""}</Td>
            <Td>{formatBytes(m.size_estimate)}</Td>
            <Td className="wrap-anywhere text-xs">
              {m.labels.split(",").join(", ")}
            </Td>
            <Td>
              <Link
                to="/scans/$scanId"
                params={{ scanId: String(m.scan_id) }}
                search={{ page: 1 }}
                className="underline"
              >
                {m.scan_id}
              </Link>
            </Td>
          </Tr>
        ))}
      </Table>
      <nav className="flex items-center gap-4 p-2" aria-label="Pages">
        {pageLink(page - 1, "Previous")}
        <span>
          Page {page} of {pages}
        </span>
        {pageLink(page + 1, "Next")}
      </nav>
    </div>
  );
}
