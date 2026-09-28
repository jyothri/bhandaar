import { useQueries } from "@tanstack/react-query";
import { CSSProperties, ReactNode, useState } from "react";

import { getBrowseChildren } from "../api";
import { queryKeys } from "../api/queryKeys";
import { fileKind } from "../fileTypes";
import { formatBytes, formatCount, formatDateTime, shareOf } from "../format";
import { BrowseFile, BrowseFolder, sharedWithMe } from "../types/browse";
import Button from "./ui/Button";
import Card from "./ui/Card";
import Icon from "./ui/Icon";

// A folder's contents as a collapsible tree, loaded one folder at a time,
// as driveagent's HTML report shows a drive. Each row has an icon, and a
// bar for its share of the folder it's in. See docs/specs/browse.md,
// "Browse page", and docs/archive/ui-refresh.md, "Storage at a glance".

export type TreeSource = { kind: "google" | "agent"; key: string };

type TreeProps = {
  source: TreeSource;
  folder: string;
  // Moves the trail to a folder.
  onOpen: (folder: string) => void;
};

/** The contents of folder, subfolders first; each subfolder expands. */
export default function FolderTree({ source, folder, onOpen }: TreeProps) {
  return (
    <Card flush className="py-1 text-sm">
      <div role="tree">
        <FolderContents
          source={source}
          folder={folder}
          onOpen={onOpen}
          depth={0}
        />
      </div>
    </Card>
  );
}

// "1 file", "2 files".
function files(n: number): string {
  return `${formatCount(n)} file${n === 1 ? "" : "s"}`;
}

// Where a Drive file or folder opens.
function driveUrl(id: string, isDir: boolean): string {
  const encoded = encodeURIComponent(id);
  return isDir
    ? `https://drive.google.com/drive/folders/${encoded}`
    : `https://drive.google.com/file/d/${encoded}/view`;
}

// Indentation per level: 12 px on phones, 20 px from sm up.
const indent = (depth: number, extra = 0) =>
  ({ "--indent": depth + extra }) as CSSProperties;
const indentClass =
  "pl-[calc(var(--indent)*12px+12px)] sm:pl-[calc(var(--indent)*20px+16px)]";

/**
 * One row of the tree. On phones: name, then size (and count) on the
 * right, and the bar under the name; from sm: name, bar, size, and count
 * or date.
 */
function Row({
  depth,
  name,
  bar,
  size,
  detail,
  detailOnPhones = false,
}: {
  depth: number;
  name: ReactNode;
  bar: number | null;
  size: ReactNode;
  detail: ReactNode;
  detailOnPhones?: boolean;
}) {
  return (
    <div
      style={indent(depth)}
      className={`${indentClass} grid min-h-9 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 py-1 pr-3 pointer-coarse:min-h-10 sm:grid-cols-[minmax(0,1fr)_8rem_5.5rem_8rem] sm:pr-4`}
    >
      <span className="order-1 min-w-0">{name}</span>
      <span
        aria-hidden="true"
        className="order-3 col-span-2 h-1 overflow-hidden rounded-full bg-surface-muted sm:order-2 sm:col-span-1"
      >
        {bar !== null && (
          <span
            className="block h-full rounded-full bg-accent"
            style={{ width: `${bar}%` }}
          />
        )}
      </span>
      <span className="order-2 flex flex-col items-end tabular-nums sm:contents">
        <span className="sm:order-3 sm:text-right">{size}</span>
        <span
          className={`text-xs text-muted sm:order-4 sm:text-right sm:text-sm ${detailOnPhones ? "" : "hidden sm:inline"}`}
        >
          {detail}
        </span>
      </span>
    </div>
  );
}

function Placeholder({
  depth,
  children,
}: {
  depth: number;
  children: ReactNode;
}) {
  return (
    <div style={indent(depth, 1)} className={`${indentClass} py-2 text-muted`}>
      {children}
    </div>
  );
}

// Grey rows while a folder loads.
function Skeleton({ depth }: { depth: number }) {
  return (
    <div role="status" aria-label="Loading">
      {[60, 45, 70].map((width) => (
        <div
          key={width}
          style={indent(depth, 1)}
          className={`${indentClass} flex h-9 items-center pr-4`}
        >
          <span
            className="h-3 animate-pulse rounded bg-surface-muted motion-reduce:animate-none"
            style={{ width: `${width}%` }}
          />
        </div>
      ))}
    </div>
  );
}

function FolderContents({
  source,
  folder,
  onOpen,
  depth,
}: TreeProps & { depth: number }) {
  // Pages loaded so far; "More" loads the next.
  const [pages, setPages] = useState(1);
  const sourceId = `${source.kind}:${source.key}`;
  const results = useQueries({
    queries: Array.from({ length: pages }, (_, i) => ({
      queryKey: queryKeys.browseChildren(sourceId, folder, i + 1),
      queryFn: () => getBrowseChildren(source.kind, source.key, folder, i + 1),
    })),
  });
  const failed = results.find((r) => r.error);
  const first = results[0].data;
  const last = results[results.length - 1].data;

  if (failed) {
    return (
      <Placeholder depth={depth}>
        <span className="text-danger">
          Couldn't load this folder: {failed.error?.message}
        </span>
      </Placeholder>
    );
  }
  if (!first) {
    return <Skeleton depth={depth} />;
  }
  if (first.entries === 0) {
    return (
      <Placeholder depth={depth}>
        {depth === 0 ? "Nothing recorded here yet." : "Empty."}
      </Placeholder>
    );
  }
  // Each entry's share of this folder.
  const total = first.totals?.bytes ?? 0;
  const more = last && last.page * last.page_size < last.entries;
  const loaded = results.flatMap((r) => (r.data ? [r.data] : []));
  return (
    <ul role="group">
      {loaded
        .flatMap((page) => page.folders)
        .map((f) => (
          <Folder
            key={f.id}
            source={source}
            folder={f}
            onOpen={onOpen}
            depth={depth}
            total={total}
          />
        ))}
      {loaded
        .flatMap((page) => page.files)
        .map((f) => (
          <File
            key={f.id}
            source={source}
            file={f}
            depth={depth}
            total={total}
          />
        ))}
      {more && (
        <li style={indent(depth, 1)} className={`${indentClass} py-2`}>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => setPages(pages + 1)}
          >
            More ({formatCount(last.entries - last.page * last.page_size)} left)
          </Button>
        </li>
      )}
      {!last && (
        <li>
          <Skeleton depth={depth} />
        </li>
      )}
    </ul>
  );
}

const rowHover = "hover:bg-surface-muted/60";

function Folder({
  source,
  folder,
  onOpen,
  depth,
  total,
}: {
  source: TreeSource;
  folder: BrowseFolder;
  onOpen: (folder: string) => void;
  depth: number;
  total: number;
}) {
  const [open, setOpen] = useState(false);
  const name = (
    <span className="group flex min-w-0 items-center gap-1.5">
      <Icon
        name="chevronRight"
        className={`shrink-0 text-muted transition-transform ${open ? "rotate-90" : ""}`}
      />
      <Icon name="folder" className="shrink-0 text-accent" />
      <a
        href="#"
        className="truncate hover:underline"
        title={source.kind === "agent" ? folder.id : folder.name}
        onClick={(e) => {
          // Moves the trail here, without toggling the folder.
          e.preventDefault();
          onOpen(folder.id);
        }}
      >
        {folder.name}
      </a>
      {source.kind === "google" && folder.id !== sharedWithMe && (
        <a
          href={driveUrl(folder.id, true)}
          target="_blank"
          rel="noreferrer"
          className="shrink-0 text-muted opacity-0 group-hover:opacity-100 hover:text-accent focus:opacity-100 pointer-coarse:opacity-100"
          title="Open in Google Drive"
          aria-label={`Open ${folder.name} in Google Drive`}
          onClick={(e) => e.stopPropagation()}
        >
          <Icon name="externalLink" size={14} />
        </a>
      )}
    </span>
  );
  return (
    <li role="treeitem" aria-expanded={open}>
      <details onToggle={(e) => setOpen(e.currentTarget.open)}>
        <summary
          className={`cursor-pointer list-none [&::-webkit-details-marker]:hidden ${rowHover}`}
        >
          <Row
            depth={depth}
            name={name}
            bar={shareOf(folder.bytes, total)}
            size={formatBytes(folder.bytes)}
            detail={files(folder.files)}
            detailOnPhones
          />
        </summary>
        {open && (
          <FolderContents
            source={source}
            folder={folder.id}
            onOpen={onOpen}
            depth={depth + 1}
          />
        )}
      </details>
    </li>
  );
}

function File({
  source,
  file,
  depth,
  total,
}: {
  source: TreeSource;
  file: BrowseFile;
  depth: number;
  total: number;
}) {
  const size = file.error ? (
    <span className="text-danger" title={file.error}>
      unreadable
    </span>
  ) : file.size === null ? (
    <span className="text-muted">not scanned</span>
  ) : (
    formatBytes(file.size)
  );
  const name = (
    <span className="flex min-w-0 items-center gap-1.5">
      {/* Lines up with folders' names, past their chevron. */}
      <span className="w-4 shrink-0" />
      <Icon
        name={fileKind(file.name, file.mime_type)}
        className="shrink-0 text-muted"
      />
      <span className="min-w-0">
        {source.kind === "google" ? (
          <a
            href={driveUrl(file.id, false)}
            target="_blank"
            rel="noreferrer"
            className="block truncate hover:underline"
            title={file.name}
          >
            {file.name}
          </a>
        ) : (
          <span className="block truncate" title={file.id}>
            {file.name}
          </span>
        )}
        {file.error && (
          <span className="block text-xs text-danger">{file.error}</span>
        )}
      </span>
    </span>
  );
  return (
    <li role="treeitem" className={rowHover}>
      <Row
        depth={depth}
        name={name}
        bar={file.size === null ? null : shareOf(file.size, total)}
        size={size}
        detail={file.modified ? formatDateTime(file.modified) : ""}
      />
    </li>
  );
}
