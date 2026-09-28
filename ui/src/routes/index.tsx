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
import Badge from "../components/ui/Badge";
import Card from "../components/ui/Card";
import Icon, { IconName } from "../components/ui/Icon";
import Pager from "../components/ui/Pager";
import Select from "../components/ui/Select";
import Spinner from "../components/ui/Spinner";
import Table from "../components/ui/Table";
import Tabs from "../components/ui/Tabs";
import { buttonClasses } from "../components/ui/styles";
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
      <p className="py-6 text-sm text-danger">
        Couldn't load what you can browse: {error.message}
      </p>
    );
  }
  if (!sources) {
    return <p className="py-6 text-sm text-muted">Loading…</p>;
  }
  if (sources.length === 0) {
    return (
      <Card className="mt-6 text-center">
        <p className="font-semibold">Nothing to browse yet</p>
        <p className="mt-1 text-sm text-muted">
          Link a Google account and scan it, or upload a drive's scans with{" "}
          <code className="rounded bg-surface-muted px-1 py-0.5 text-xs">
            driveagent
          </code>
          .
        </p>
        <Link
          to="/request"
          search={{}}
          className={`${buttonClasses("primary")} mt-4`}
        >
          Link a Google account
        </Link>
      </Card>
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
      <div className="space-y-4">
        <label className="flex flex-col gap-1.5 sm:flex-row sm:items-center sm:gap-3">
          <span className="text-sm font-medium">Source</span>
          <Select
            value={id}
            onChange={(e) => navigate({ search: { source: e.target.value } })}
            className="w-full sm:w-80"
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
          </Select>
        </label>
        {source.kind === "google" ? (
          <GoogleAccount
            source={source}
            label={label(source)}
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
    <Link to="/" search={{}}>
      Browse
    </Link>,
    <Link to="/" search={{ source: id }}>
      {label}
    </Link>,
  ];
  if (source.kind === "google") {
    items.push(
      <Link to="/" search={{ source: id, service }}>
        {serviceNames[service]}
      </Link>
    );
  }
  for (const part of path) {
    items.push(
      <Link to="/" search={{ source: id, ...serviceSearch, folder: part.id }}>
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

// "504 files", "1 message".
function count(n: number, noun: string): string {
  return `${formatCount(n)} ${noun}${n === 1 ? "" : "s"}`;
}

// "504 files · 3.7 GB"
function totals(files: number, bytes: number, noun = "file"): string {
  return `${count(files, noun)} · ${formatBytes(bytes)}`;
}

function Updating() {
  return (
    <Badge tone="warning">
      <Spinner size={12} />
      Updating totals…
    </Badge>
  );
}

// A source's totals at a glance: its name, the total size large, and
// the count and when it was last updated.
function SummaryCard({
  icon,
  name,
  kind,
  badges,
  children,
  footer,
}: {
  icon: IconName;
  name: string;
  kind: string;
  badges?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
}) {
  return (
    <Card>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 items-center gap-3">
          <span className="grid size-10 shrink-0 place-items-center rounded-lg bg-accent-soft text-accent">
            <Icon name={icon} size={20} />
          </span>
          <div className="min-w-0">
            <h2 className="truncate font-semibold">{name}</h2>
            <p className="text-xs text-muted">{kind}</p>
          </div>
        </div>
        {badges && <div className="flex flex-wrap gap-2">{badges}</div>}
      </div>
      <div className="mt-4">{children}</div>
      {footer && <div className="mt-4 border-t border-line pt-3">{footer}</div>}
    </Card>
  );
}

// The big number and the line under it.
function Figures({
  bytes,
  files,
  noun,
  updatedAt,
}: {
  bytes: number;
  files: number;
  noun: string;
  updatedAt?: string;
}) {
  return (
    <>
      <p className="text-3xl font-semibold tracking-tight tabular-nums">
        {formatBytes(bytes)}
      </p>
      <p className="mt-1 text-sm text-muted">
        {count(files, noun)}
        {updatedAt && ` · Updated ${formatAgo(updatedAt)}`}
      </p>
    </>
  );
}

function GoogleAccount({
  source,
  label,
  service,
  folder,
  search,
}: {
  source: BrowseSource;
  label: string;
  service: BrowseService;
  folder: string;
  search: BrowseSearch;
}) {
  const navigate = useNavigate({ from: Route.fullPath });
  const services = source.services!;
  const sub = (name: BrowseService, st: ServiceTotals) =>
    st.files !== undefined && st.bytes !== undefined
      ? totals(st.files, st.bytes, name === "gmail" ? "message" : "file")
      : st.granted
        ? "Not scanned"
        : "Not granted";
  const current = services[service];
  const recorded = current.files !== undefined && current.bytes !== undefined;
  return (
    <>
      <Tabs<BrowseService | "photos">
        label="Service"
        value={service}
        onChange={(name) =>
          name !== "photos" &&
          navigate({ search: { source: sourceId(source), service: name } })
        }
        items={[
          {
            id: "drive",
            label: "Google Drive",
            sub: sub("drive", services.drive),
          },
          { id: "gmail", label: "Gmail", sub: sub("gmail", services.gmail) },
          { id: "photos", label: "Google Photos", sub: "Soon", disabled: true },
        ]}
      />
      <SummaryCard
        icon={service === "gmail" ? "mail" : "cloud"}
        name={label}
        kind={`Google account · ${serviceNames[service]}`}
        badges={service === "drive" && current.updating && <Updating />}
      >
        {recorded ? (
          <Figures
            bytes={current.bytes!}
            files={current.files!}
            noun={service === "gmail" ? "message" : "file"}
            updatedAt={current.updated_at}
          />
        ) : (
          <NotRecorded
            source={source}
            service={service}
            granted={current.granted}
          />
        )}
      </SummaryCard>
      {!recorded ? null : service === "drive" ? (
        <FolderTree
          source={{ kind: "google", key: source.key }}
          folder={folder}
          onOpen={(f) =>
            navigate({
              search: { source: sourceId(source), service, folder: f },
            })
          }
        />
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
    <div className="flex flex-col items-start gap-3">
      <p className="text-sm text-muted">
        {granted
          ? `This account's ${serviceNames[service]} hasn't been scanned yet.`
          : `This account hasn't granted ${serviceNames[service]} access yet.`}
      </p>
      <Link
        to="/request"
        search={{ type: service, account: source.key }}
        className={buttonClasses("primary")}
      >
        {granted ? "Scan it" : "Grant access"} on the Request page
      </Link>
    </div>
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
      <SummaryCard
        icon="hardDrive"
        name={source.name}
        kind="Drive, uploaded by driveagent"
        badges={
          <>
            {source.physical_drive !== undefined && (
              <Badge tone="accent">
                Linked copy of physical drive {source.physical_drive}
              </Badge>
            )}
            {source.updating && <Updating />}
          </>
        }
        footer={<AgentStatus driveKey={source.key} />}
      >
        <Figures
          bytes={source.bytes ?? 0}
          files={source.files ?? 0}
          noun="file"
          updatedAt={source.updated_at}
        />
      </SummaryCard>
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
      <p className="text-sm text-danger">
        Couldn't load the drive's status: {error.message}
      </p>
    );
  }
  if (!status) {
    return <p className="text-sm text-muted">Loading status…</p>;
  }
  const run = status.last_scan;
  const summary = [
    run ? `Last scan ${formatAgo(run.started_at)}` : "Never scanned",
    status.last_synced_at
      ? `synced ${formatAgo(status.last_synced_at)}`
      : "never synced",
  ].join(" · ");
  const outcome = !run ? null : run.finished_at === null ? (
    run.interrupted ? (
      <Badge tone="danger">Interrupted</Badge>
    ) : (
      <Badge tone="warning">Running or cut off</Badge>
    )
  ) : run.interrupted ? (
    <Badge tone="danger">Interrupted</Badge>
  ) : (
    <Badge tone="success">Completed</Badge>
  );
  return (
    <details className="group text-sm">
      <summary className="flex cursor-pointer list-none items-center gap-1.5 text-muted hover:text-fg [&::-webkit-details-marker]:hidden">
        <Icon
          name="chevronRight"
          className="transition-transform group-open:rotate-90"
        />
        {summary}
      </summary>
      <dl className="mt-3 grid gap-x-6 gap-y-2 sm:grid-cols-[max-content_1fr]">
        {run && (
          <>
            <dt className="text-muted">Last scan</dt>
            <dd className="flex flex-wrap items-center gap-2">
              {formatDateTime(run.started_at)}
              {run.finished_at && ` to ${formatDateTime(run.finished_at)}`}
              {outcome}
              {run.files_seen !== null && (
                <span className="text-muted">
                  {count(run.files_seen, "file")} seen
                </span>
              )}
            </dd>
          </>
        )}
        <dt className="text-muted">Last sync</dt>
        <dd>
          {status.last_synced_at
            ? formatDateTime(status.last_synced_at)
            : "Never"}
        </dd>
        {status.physical_drive !== null && (
          <>
            <dt className="text-muted">Physical drive</dt>
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
  // A two-way segmented control of links, so each order has its URL.
  const sortLink = (to: "size" | "date", text: string) => (
    <Link
      from={Route.fullPath}
      search={(prev) => ({ ...prev, sort: to, page: undefined })}
      aria-current={to === sort ? "true" : undefined}
      className={`rounded px-2.5 py-1 ${
        to === sort
          ? "bg-surface font-medium text-fg shadow-sm"
          : "text-muted hover:text-fg"
      }`}
    >
      {text}
    </Link>
  );
  const actions = (
    <>
      <div
        role="group"
        aria-label="Order"
        className="inline-flex rounded-md bg-surface-muted p-0.5 text-sm"
      >
        {sortLink("size", "Largest first")}
        {sortLink("date", "Newest first")}
      </div>
      <MaskToggle />
    </>
  );
  const pages = data ? Math.max(1, Math.ceil(data.total / data.page_size)) : 1;
  return (
    <Card title="Messages" actions={actions}>
      <p className="mb-3 text-xs text-muted">
        Every message this account's Gmail scans found. Messages deleted in
        Gmail since are still listed.
      </p>
      {error ? (
        <p className="text-sm text-danger">
          Couldn't load messages: {error.message}
        </p>
      ) : !data ? (
        <p className="text-sm text-muted">Loading messages…</p>
      ) : (
        <>
          <Pager
            page={page}
            pages={pages}
            link={(to, children, className) => (
              <Link
                from={Route.fullPath}
                search={(prev) => ({ ...prev, page: to > 1 ? to : undefined })}
                className={className}
              >
                {children}
              </Link>
            )}
          />
          <Table
            rowKey={(m) => m.message_metadata_id}
            rows={data.messages}
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
              {
                header: "Labels",
                cell: (m) => (
                  <span className="flex flex-wrap justify-end gap-1 sm:justify-start">
                    {m.labels
                      .split(",")
                      .filter(Boolean)
                      .map((l) => (
                        <Badge key={l}>{l}</Badge>
                      ))}
                  </span>
                ),
              },
              {
                header: "Scan",
                numeric: true,
                cell: (m) => (
                  <Link
                    to="/scans/$scanId"
                    params={{ scanId: String(m.scan_id) }}
                    search={{ page: 1 }}
                    className="text-accent hover:underline"
                  >
                    {m.scan_id}
                  </Link>
                ),
              },
            ]}
          />
        </>
      )}
    </Card>
  );
}
