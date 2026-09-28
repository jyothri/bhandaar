import { useQueries } from "@tanstack/react-query";
import { useState } from "react";

import { getBrowseChildren } from "../api";
import { queryKeys } from "../api/queryKeys";
import { formatBytes, formatCount, formatDateTime } from "../format";
import { BrowseFile, BrowseFolder, sharedWithMe } from "../types/browse";

// A folder's contents as a collapsible tree, loaded one folder at a time,
// as driveagent's HTML report shows a drive. See docs/specs/browse.md,
// "Browse page".

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
    <div role="tree" className="p-2 text-sm">
      <FolderContents
        source={source}
        folder={folder}
        onOpen={onOpen}
        depth={0}
      />
    </div>
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

const row = "grid grid-cols-[1fr_7rem_9rem] gap-2 py-0.5";

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
  const last = results[results.length - 1].data;
  const indent = { paddingLeft: `${depth * 1.25}rem` };

  if (failed) {
    return (
      <p style={indent} className="text-red-500">
        Couldn't load this folder: {failed.error?.message}
      </p>
    );
  }
  if (!results[0].data) {
    return (
      <p style={indent} className="text-gray-500">
        Loading…
      </p>
    );
  }
  if (results[0].data.entries === 0) {
    return (
      <p style={indent} className="text-gray-500">
        {depth === 0 ? "Nothing recorded here yet." : "Empty."}
      </p>
    );
  }
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
          />
        ))}
      {loaded
        .flatMap((page) => page.files)
        .map((f) => (
          <File key={f.id} source={source} file={f} depth={depth} />
        ))}
      {more && (
        <li style={indent}>
          <button
            type="button"
            className="underline py-1"
            onClick={() => setPages(pages + 1)}
          >
            More ({formatCount(last.entries - last.page * last.page_size)} left)
          </button>
        </li>
      )}
      {!last && (
        <li style={indent} className="text-gray-500">
          Loading…
        </li>
      )}
    </ul>
  );
}

function Folder({
  source,
  folder,
  onOpen,
  depth,
}: {
  source: TreeSource;
  folder: BrowseFolder;
  onOpen: (folder: string) => void;
  depth: number;
}) {
  const [open, setOpen] = useState(false);
  return (
    <li role="treeitem" aria-expanded={open}>
      <details onToggle={(e) => setOpen(e.currentTarget.open)}>
        <summary
          className={`${row} cursor-pointer list-none hover:bg-gray-50 dark:hover:bg-gray-800`}
          style={{ paddingLeft: `${depth * 1.25}rem` }}
        >
          <span className="wrap-anywhere">
            <span aria-hidden="true" className="inline-block w-4 text-gray-500">
              {open ? "▾" : "▸"}
            </span>
            <a
              href="#"
              className="underline"
              title={source.kind === "agent" ? folder.id : undefined}
              onClick={(e) => {
                // Moves the trail here, without toggling the folder.
                e.preventDefault();
                onOpen(folder.id);
              }}
            >
              {folder.name}/
            </a>
            {source.kind === "google" && folder.id !== sharedWithMe && (
              <a
                href={driveUrl(folder.id, true)}
                target="_blank"
                rel="noreferrer"
                className="ml-2 text-gray-500"
                title="Open in Google Drive"
                onClick={(e) => e.stopPropagation()}
              >
                ↗
              </a>
            )}
          </span>
          <span className="text-right">{formatBytes(folder.bytes)}</span>
          <span className="text-right text-gray-500">
            {files(folder.files)}
          </span>
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
}: {
  source: TreeSource;
  file: BrowseFile;
  depth: number;
}) {
  const size = file.error ? (
    <span className="text-red-500" title={file.error}>
      unreadable
    </span>
  ) : file.size === null ? (
    <span className="text-gray-500">not scanned</span>
  ) : (
    formatBytes(file.size)
  );
  return (
    <li
      role="treeitem"
      className={row}
      style={{ paddingLeft: `${depth * 1.25 + 1}rem` }}
    >
      <span className="wrap-anywhere">
        {source.kind === "google" ? (
          <a
            href={driveUrl(file.id, false)}
            target="_blank"
            rel="noreferrer"
            className="underline"
          >
            {file.name}
          </a>
        ) : (
          <span title={file.id}>{file.name}</span>
        )}
        {file.error && (
          <span className="block text-xs text-red-500">{file.error}</span>
        )}
      </span>
      <span className="text-right">{size}</span>
      <span className="text-right text-gray-500">
        {file.modified ? formatDateTime(file.modified) : ""}
      </span>
    </li>
  );
}
