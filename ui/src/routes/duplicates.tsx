import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useState } from "react";

import { getDupGroups, getDupMembers, getDupSummary } from "../api";
import { queryKeys } from "../api/queryKeys";
import Badge from "../components/ui/Badge";
import Button from "../components/ui/Button";
import Card from "../components/ui/Card";
import Checkbox from "../components/ui/Checkbox";
import Icon, { IconName } from "../components/ui/Icon";
import Pager from "../components/ui/Pager";
import Select from "../components/ui/Select";
import Spinner from "../components/ui/Spinner";
import Tabs from "../components/ui/Tabs";
import { fileKind } from "../fileTypes";
import { formatAgo, formatBytes, formatCount, formatDateTime } from "../format";
import {
  DupFilter,
  DupGroup,
  DupKind,
  DupMember,
  DupSource,
  DupSummary,
} from "../types/duplicates";

// Duplicates: identical files and folders across every source, and likely
// copies of picked Google Photos. The server works them out ahead of time;
// this page reads that index. See docs/archive/duplicates.md, "Duplicates
// page".

type DuplicatesSearch = {
  kind?: DupKind;
  source?: DupSource;
  across?: boolean;
  min_size?: number;
  hide_same_physical?: boolean;
  page?: number;
};

const minSizes = [
  { value: 0, label: "Any size" },
  { value: 1 << 20, label: "1 MB or more" },
  { value: 10 << 20, label: "10 MB or more" },
  { value: 100 << 20, label: "100 MB or more" },
  { value: 1 << 30, label: "1 GB or more" },
];

export const Route = createFileRoute("/duplicates")({
  component: Duplicates,
  validateSearch: (search: Record<string, unknown>): DuplicatesSearch => {
    const page = Number(search.page);
    const minSize = Number(search.min_size);
    const flag = (v: unknown) => v === true || v === "1" || v === 1;
    return {
      ...(search.kind === "folder" || search.kind === "photo"
        ? { kind: search.kind }
        : {}),
      ...(typeof search.source === "string" && search.source !== ""
        ? { source: search.source }
        : {}),
      ...(flag(search.across) ? { across: true } : {}),
      ...(minSizes.some((m) => m.value > 0 && m.value === minSize)
        ? { min_size: minSize }
        : {}),
      ...(flag(search.hide_same_physical) ? { hide_same_physical: true } : {}),
      ...(Number.isInteger(page) && page > 1 ? { page } : {}),
    };
  },
});

const kindTabs: { id: DupKind; label: string }[] = [
  { id: "file", label: "Files" },
  { id: "folder", label: "Folders" },
  { id: "photo", label: "Photos (likely)" },
];

// "1 file", "2 files".
function count(n: number, noun: string): string {
  return `${formatCount(n)} ${noun}${n === 1 ? "" : "s"}`;
}

/** A source's Browse page, at folder when given. */
function browseSearch(source: DupSource, folder?: string) {
  if (source.startsWith("agent:")) {
    return { source, ...(folder ? { folder } : {}) };
  }
  // google:<client key>:<service>[:<bucket>]
  const [, clientKey, service] = source.split(":");
  return {
    source: `google:${clientKey}`,
    service: service as "drive" | "gcs" | "photos",
    ...(folder && service !== "photos" ? { folder } : {}),
  };
}

/** Where a copy lives outside Bhandaar: Drive, or the Cloud console. */
function externalLink(kind: DupKind, m: DupMember): string | undefined {
  if (m.source.endsWith(":drive")) {
    return kind === "folder"
      ? `https://drive.google.com/drive/folders/${encodeURIComponent(m.item)}`
      : `https://drive.google.com/file/d/${encodeURIComponent(m.item)}/view`;
  }
  const gcs = m.source.indexOf(":gcs:");
  if (gcs >= 0 && kind !== "folder") {
    const bucket = m.source.slice(gcs + 5);
    return `https://console.cloud.google.com/storage/browser/_details/${encodeURIComponent(bucket)}/${m.item
      .split("/")
      .map(encodeURIComponent)
      .join("/")}`;
  }
  return undefined;
}

function sourceIcon(source: DupSource): IconName {
  if (source.startsWith("agent:")) {
    return "hardDrive";
  }
  if (source.endsWith(":photos")) {
    return "image";
  }
  return "cloud";
}

function Duplicates() {
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const kind = search.kind ?? "file";
  const summary = useQuery({
    queryKey: queryKeys.dupSummary,
    queryFn: getDupSummary,
    // Watch a build until it ends, and wait for the first.
    refetchInterval: (q) => {
      const s = q.state.data;
      return s && (s.updating || (!s.built_at && !s.error)) ? 5000 : false;
    },
  });

  // Changing a filter starts again at page 1.
  const setFilter = (change: Partial<DuplicatesSearch>) =>
    navigate({
      search: (prev) => {
        const next: DuplicatesSearch = { ...prev, ...change, page: undefined };
        for (const key of Object.keys(next) as (keyof DuplicatesSearch)[]) {
          if (
            next[key] === undefined ||
            next[key] === false ||
            next[key] === 0
          ) {
            delete next[key];
          }
        }
        return next;
      },
    });

  if (summary.error) {
    return (
      <p className="py-6 text-sm text-danger">
        Couldn't load your duplicates: {summary.error.message}
      </p>
    );
  }
  if (!summary.data) {
    return <p className="py-6 text-sm text-muted">Loading…</p>;
  }
  const s = summary.data;

  return (
    <div className="space-y-4 pt-6">
      <h1 className="text-xl font-semibold">Duplicates</h1>
      <SummaryCard summary={s} />
      {s.built_at && (
        <>
          <Tabs
            label="Kind"
            items={kindTabs.map((t) => ({
              id: t.id,
              label: t.label,
              sub: formatCount(s.groups[t.id]),
            }))}
            value={kind}
            onChange={(id) =>
              setFilter({ kind: id === "file" ? undefined : id })
            }
          />
          <Filters
            summary={s}
            search={search}
            kind={kind}
            setFilter={setFilter}
          />
          <Groups
            filter={{
              kind,
              source: search.source,
              across: search.across,
              min_size: search.min_size,
              hide_same_physical: search.hide_same_physical,
              page: search.page,
            }}
            builtAt={s.built_at}
          />
          {kind === "folder" && s.uncomparable.length > 0 && (
            <Uncomparables summary={s} />
          )}
        </>
      )}
    </div>
  );
}

function SummaryCard({ summary: s }: { summary: DupSummary }) {
  if (!s.built_at) {
    return (
      <Card>
        {s.error && !s.updating ? (
          <p className="text-sm text-danger">
            Couldn't find your duplicates: {s.error}
          </p>
        ) : (
          <p className="flex items-center gap-2 text-sm text-muted">
            <Spinner />
            Finding your duplicates. This takes a minute or two.
          </p>
        )}
      </Card>
    );
  }
  const groups = s.groups.file + s.groups.folder;
  return (
    <Card>
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <span className="text-2xl font-semibold tabular-nums">
          {formatBytes(s.reclaimable)}
        </span>
        <span className="text-sm text-muted">reclaimable</span>
        <span className="text-sm text-muted tabular-nums">
          · {count(groups, "group")} · Updated {formatAgo(s.built_at)}
        </span>
        {s.updating && (
          <Badge tone="accent">
            <Spinner size={12} />
            Updating…
          </Badge>
        )}
      </div>
      {s.error && !s.updating && (
        <p className="mt-1 text-sm text-danger">
          Couldn't update
          {s.failed_at ? ` ${formatAgo(s.failed_at)}` : ""}: {s.error}
        </p>
      )}
      {s.by_source.length > 0 && (
        <ul className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-sm">
          {s.by_source.map((t) => (
            <li key={t.source}>
              <Link
                to="/duplicates"
                search={{ source: t.source }}
                className="text-muted hover:text-fg hover:underline"
                title={`${count(t.files, "file")} with a copy elsewhere`}
              >
                {t.label}{" "}
                <span className="text-fg tabular-nums">
                  {formatBytes(t.bytes)}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

function Filters({
  summary,
  search,
  kind,
  setFilter,
}: {
  summary: DupSummary;
  search: DuplicatesSearch;
  kind: DupKind;
  setFilter: (change: Partial<DuplicatesSearch>) => void;
}) {
  // The sources with duplicates, and the one picked even if it has none.
  const sources = summary.by_source.map((t) => ({
    source: t.source,
    label: t.label,
  }));
  if (search.source && !sources.some((t) => t.source === search.source)) {
    sources.push({ source: search.source, label: search.source });
  }
  const google = sources.filter((t) => t.source.startsWith("google:"));
  const agents = sources.filter((t) => t.source.startsWith("agent:"));
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
      <Select
        aria-label="Source"
        value={search.source ?? ""}
        onChange={(e) => setFilter({ source: e.target.value || undefined })}
        className="w-full sm:w-auto sm:min-w-56"
      >
        <option value="">All sources</option>
        {google.length > 0 && (
          <optgroup label="Google">
            {google.map((t) => (
              <option key={t.source} value={t.source}>
                {t.label}
              </option>
            ))}
          </optgroup>
        )}
        {agents.length > 0 && (
          <optgroup label="Agent drives">
            {agents.map((t) => (
              <option key={t.source} value={t.source}>
                {t.label}
              </option>
            ))}
          </optgroup>
        )}
      </Select>
      <Select
        aria-label="Minimum size"
        value={search.min_size ?? 0}
        onChange={(e) =>
          setFilter({ min_size: Number(e.target.value) || undefined })
        }
      >
        {minSizes.map((m) => (
          <option key={m.value} value={m.value}>
            {m.label}
          </option>
        ))}
      </Select>
      {kind !== "photo" && (
        <>
          <Checkbox
            label="Across sources only"
            checked={search.across ?? false}
            onChange={(e) => setFilter({ across: e.target.checked })}
          />
          <Checkbox
            label="Hide same physical drive"
            checked={search.hide_same_physical ?? false}
            onChange={(e) =>
              setFilter({ hide_same_physical: e.target.checked })
            }
          />
        </>
      )}
    </div>
  );
}

function Groups({ filter, builtAt }: { filter: DupFilter; builtAt: string }) {
  const { data, error, isPending } = useQuery({
    // A new index is new groups.
    queryKey: [...queryKeys.dupGroups(filter), builtAt],
    queryFn: () => getDupGroups(filter),
    placeholderData: (previous) => previous,
  });
  if (error) {
    return (
      <p className="text-sm text-danger">
        Couldn't load the duplicates: {error.message}
      </p>
    );
  }
  if (isPending || !data) {
    return <p className="text-sm text-muted">Loading…</p>;
  }
  if (data.total === 0) {
    return (
      <Card>
        <p className="text-sm text-muted">
          {filter.kind === "photo"
            ? "No picked photos match files elsewhere."
            : `No duplicate ${filter.kind === "folder" ? "folders" : "files"} match these filters.`}
        </p>
      </Card>
    );
  }
  const pages = Math.max(1, Math.ceil(data.total / data.page_size));
  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="pb-3 text-sm text-muted tabular-nums">
          {count(data.total, "group")}
        </p>
        <Pager
          page={data.page}
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
      </div>
      <Card flush>
        <ul className="divide-y divide-line">
          {data.groups.map((g) => (
            <GroupRow
              key={`${g.kind}:${g.key}`}
              group={g}
              labels={data.labels}
              builtAt={builtAt}
            />
          ))}
        </ul>
      </Card>
    </div>
  );
}

function GroupRow({
  group: g,
  labels,
  builtAt,
}: {
  group: DupGroup;
  labels: Record<DupSource, string>;
  builtAt: string;
}) {
  const [open, setOpen] = useState(false);
  const icon: IconName = g.kind === "folder" ? "folder" : fileKind(g.name);
  return (
    <li>
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className="flex w-full flex-wrap items-start gap-x-3 gap-y-1 px-3 py-2.5 text-left text-sm hover:bg-surface-muted/40 sm:flex-nowrap sm:items-center sm:px-4"
      >
        <span className="flex min-w-0 flex-1 basis-full items-center gap-2 sm:basis-auto">
          <Icon
            name="chevronRight"
            className={`shrink-0 text-muted transition-transform ${open ? "rotate-90" : ""}`}
          />
          <Icon name={icon} className="shrink-0 text-muted" />
          <span className="truncate font-medium" title={g.name}>
            {g.name}
          </span>
        </span>
        <span className="pl-12 text-muted tabular-nums sm:pl-0 sm:whitespace-nowrap">
          {formatBytes(g.size)} × {g.copies}
          {g.kind === "folder" && ` · ${count(g.files, "file")}`}
        </span>
        <span className="tabular-nums sm:w-36 sm:text-right sm:whitespace-nowrap">
          {g.kind === "photo" ? (
            <Badge tone="warning">Likely</Badge>
          ) : g.same_physical ? (
            <Badge>Same physical drive</Badge>
          ) : (
            <>{formatBytes(g.reclaimable)} reclaimable</>
          )}
        </span>
        <span className="flex basis-full flex-wrap gap-1 pl-12 sm:basis-auto sm:pl-0">
          {g.sources.map((s) => (
            <Badge key={s}>{labels[s] ?? s}</Badge>
          ))}
        </span>
      </button>
      {open && <Members group={g} builtAt={builtAt} />}
    </li>
  );
}

function Members({ group: g, builtAt }: { group: DupGroup; builtAt: string }) {
  // The first 10 come with the group; the rest on demand.
  const [all, setAll] = useState(false);
  const rest = useQuery({
    queryKey: queryKeys.dupMembers(g.kind, g.key, 1, builtAt),
    queryFn: () => getDupMembers(g.kind, g.key, 1),
    enabled: all,
  });
  const members = all && rest.data ? rest.data.members : g.members;
  const more = g.copies - members.length;
  return (
    <div className="border-t border-line bg-surface-muted/30 px-3 py-2 sm:px-4">
      {g.kind === "photo" && g.match && (
        <p className="pb-1 text-xs text-muted">Matched on {g.match}.</p>
      )}
      <ul className="space-y-1.5 text-sm">
        {members.map((m) => (
          <MemberRow key={`${m.source}\0${m.item}`} kind={g.kind} member={m} />
        ))}
      </ul>
      {more > 0 && (
        <div className="pt-2">
          {rest.error ? (
            <p className="text-sm text-danger">
              Couldn't load the other copies: {rest.error.message}
            </p>
          ) : (
            <Button
              variant="secondary"
              size="sm"
              disabled={all}
              onClick={() => setAll(true)}
            >
              {all ? <Spinner size={12} /> : null}
              Show {formatCount(more)} more {more === 1 ? "copy" : "copies"}
            </Button>
          )}
        </div>
      )}
    </div>
  );
}

function MemberRow({ kind, member: m }: { kind: DupKind; member: DupMember }) {
  const external = externalLink(kind, m);
  return (
    <li className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
      <span className="flex shrink-0 items-center gap-1.5 text-muted">
        <Icon name={sourceIcon(m.source)} />
        {m.label}
      </span>
      <span className="min-w-0 flex-1 break-all">
        {m.path}
        {m.shared && (
          <span className="ml-2 align-middle">
            <Badge>Shared with you</Badge>
          </span>
        )}
      </span>
      {kind === "photo" && (
        <span className="text-muted tabular-nums">{formatBytes(m.size)}</span>
      )}
      {m.modified && (
        <span className="text-muted tabular-nums" title="Modified">
          {formatDateTime(m.modified)}
        </span>
      )}
      <span className="flex gap-3">
        <Link
          to="/"
          search={browseSearch(m.source, m.folder)}
          className="text-accent hover:underline"
        >
          Browse
        </Link>
        {external && (
          <a
            href={external}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1 text-accent hover:underline"
          >
            {m.source.endsWith(":drive") ? "Drive" : "Console"}
            <Icon name="externalLink" />
          </a>
        )}
      </span>
    </li>
  );
}

function Uncomparables({ summary }: { summary: DupSummary }) {
  const total = summary.uncomparable.reduce((n, u) => n + u.folders, 0);
  return (
    <details className="rounded-lg border border-line bg-surface p-3 text-sm sm:p-4">
      <summary className="cursor-pointer text-muted">
        {count(total, "folder")} couldn't be compared
      </summary>
      <ul className="mt-2 space-y-1">
        {summary.uncomparable.map((u) => (
          <li key={u.source}>
            <span className="font-medium">{u.label}</span>:{" "}
            {count(u.folders, "folder")}. {u.reason}
          </li>
        ))}
      </ul>
    </details>
  );
}
