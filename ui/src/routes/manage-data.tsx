import {
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { FormEvent, ReactNode, useRef, useState } from "react";

import {
  deleteAgentDrive,
  deleteServiceData,
  disconnectAccount,
  getDeletion,
  getSettingsData,
} from "../api";
import { queryKeys } from "../api/queryKeys";
import Button from "../components/ui/Button";
import Card from "../components/ui/Card";
import Dialog from "../components/ui/Dialog";
import Icon from "../components/ui/Icon";
import Input from "../components/ui/Input";
import Spinner from "../components/ui/Spinner";
import { formatAgo, formatBytes, formatCount } from "../format";
import {
  DeletionJob,
  RecordedService,
  SettingsAccount,
  SettingsDrive,
} from "../types/settings";

// Manage data: linked Google accounts and uploaded drives, and deleting
// them.
// Each deletion is confirmed, then runs as a job on the server; the page
// watches it until it ends. See docs/specs/data-deletion.md.

export const Route = createFileRoute("/manage-data")({
  component: ManageData,
});

// "1 file", "2 files".
function count(n: number, noun: string): string {
  return `${formatCount(n)} ${noun}${n === 1 ? "" : "s"}`;
}

// Each service's name, what it records, and the count key a deletion of
// it reports them under.
const serviceNames: Record<RecordedService, [string, string, string]> = {
  gmail: ["Gmail", "message", "messages"],
  drive: ["Google Drive", "file", "drive_items"],
  gcs: ["Cloud Storage", "object", "gcs_objects"],
  photos: ["Google Photos", "item", "photos_items"],
};

// What a finished job did, in a sentence or two.
function resultText(job: DeletionJob): {
  tone: "ok" | "warn" | "error";
  text: string;
} {
  if (job.status === "failed") {
    return {
      tone: "error",
      text: `Couldn't delete ${job.label}: ${job.error}`,
    };
  }
  const c = job.counts;
  if (job.kind === "agent_drive") {
    return {
      tone: "ok",
      text: `Deleted ${job.label}: ${count(c.files ?? 0, "file")}.`,
    };
  }
  if (job.kind in serviceNames) {
    const [, noun, key] = serviceNames[job.kind as RecordedService];
    // Drive's count includes its folders.
    const what = job.kind === "drive" ? "item" : noun;
    return {
      tone: "ok",
      text: `Deleted ${job.label}: ${count(c[key] ?? 0, what)}, ${count(c.scans ?? 0, "scan")}.`,
    };
  }
  const deleted = [
    count(c.scans ?? 0, "scan"),
    count(c.messages ?? 0, "message"),
    count(c.drive_items ?? 0, "Drive item"),
    count(c.gcs_objects ?? 0, "Cloud Storage object"),
  ].join(", ");
  const revoke = job.revoke ?? "";
  if (revoke.startsWith("failed")) {
    return {
      tone: "warn",
      text: `Disconnected ${job.label} and deleted ${deleted}, but couldn't revoke its access at Google (${revoke.replace(/^failed: /, "")}). Remove Bhandaar at myaccount.google.com/permissions.`,
    };
  }
  const how =
    revoke === "already revoked"
      ? "Its access at Google was already revoked."
      : "Its access at Google is revoked.";
  return {
    tone: "ok",
    text: `Disconnected ${job.label} and deleted ${deleted}. ${how}`,
  };
}

type Pending =
  | { kind: "drive"; drive: SettingsDrive }
  | { kind: "service"; account: SettingsAccount; service: RecordedService }
  | { kind: "account"; account: SettingsAccount };

function ManageData() {
  const queryClient = useQueryClient();
  const { data, error } = useQuery({
    queryKey: queryKeys.settingsData,
    queryFn: getSettingsData,
    // While a deletion runs, its target shows it.
    refetchInterval: (query) =>
      query.state.data &&
      [...query.state.data.accounts, ...query.state.data.drives].some(
        (x) => x.job
      )
        ? 2000
        : false,
  });
  const [pending, setPending] = useState<Pending | null>(null);
  // Jobs started here, and those whose results were dismissed.
  const [watching, setWatching] = useState<number[]>([]);
  const [dismissed, setDismissed] = useState<number[]>([]);
  const jobs = useQueries({
    queries: watching.map((id) => ({
      queryKey: queryKeys.deletion(id),
      queryFn: async () => {
        const job = await getDeletion(id);
        if (job.status !== "running") {
          // What's gone shouldn't show anywhere.
          for (const key of [
            queryKeys.settingsData,
            queryKeys.browseSources,
            queryKeys.accounts,
            queryKeys.scannedAccounts,
            queryKeys.allScanRequests,
            ["browseChildren"],
          ]) {
            queryClient.invalidateQueries({ queryKey: key });
          }
        }
        return job;
      },
      refetchInterval: (query: { state: { data?: DeletionJob } }) =>
        query.state.data?.status === "running" ? 2000 : false,
    })),
  });
  // The ended ones, newest first.
  const results = jobs
    .flatMap((j) => (j.data && j.data.status !== "running" ? [j.data] : []))
    .filter((job) => !dismissed.includes(job.id))
    .reverse();

  function started(job: DeletionJob) {
    setPending(null);
    setWatching((ids) => (ids.includes(job.id) ? ids : [...ids, job.id]));
    queryClient.invalidateQueries({ queryKey: queryKeys.settingsData });
  }

  if (error) {
    return (
      <p className="py-6 text-sm text-danger">
        Couldn't load your accounts and drives: {error.message}
      </p>
    );
  }
  if (!data) {
    return <p className="py-6 text-sm text-muted">Loading…</p>;
  }
  // Drives by the box they were uploaded from.
  const boxes = new Map<string, SettingsDrive[]>();
  for (const d of data.drives) {
    const host = d.hostname || "Unknown machine";
    boxes.set(host, [...(boxes.get(host) ?? []), d]);
  }

  return (
    <div className="space-y-4 pt-6">
      <h1 className="text-xl font-semibold">Manage data</h1>
      {results.map((job) => {
        const { tone, text } = resultText(job);
        return (
          <p
            key={job.id}
            role="status"
            className={`flex items-start gap-2 rounded-md px-3 py-2 text-sm ${
              tone === "ok"
                ? "bg-success/10 text-success"
                : tone === "warn"
                  ? "bg-warning/10 text-warning"
                  : "bg-danger/10 text-danger"
            }`}
          >
            <Icon
              name={tone === "ok" ? "check" : "warning"}
              className="mt-0.5 shrink-0"
            />
            <span className="flex-1">{text}</span>
            <button
              type="button"
              aria-label="Dismiss"
              className="shrink-0 opacity-70 hover:opacity-100"
              onClick={() => setDismissed((ids) => [...ids, job.id])}
            >
              <Icon name="close" />
            </button>
          </p>
        );
      })}

      <Card title="Linked Google accounts">
        {data.accounts.length === 0 ? (
          <p className="text-sm text-muted">No linked accounts.</p>
        ) : (
          <ul className="divide-y divide-line">
            {data.accounts.map((a) => (
              <li key={a.client_key} className="py-3 first:pt-0 last:pb-0">
                <AccountRow account={a} onDelete={setPending} />
              </li>
            ))}
          </ul>
        )}
      </Card>

      <Card title="Uploaded drives">
        {data.drives.length === 0 ? (
          <p className="text-sm text-muted">
            No drives uploaded by driveagent.
          </p>
        ) : (
          <div className="space-y-4">
            {[...boxes].map(([host, drives]) => (
              <section key={host}>
                <h3 className="flex items-center gap-2 text-sm font-semibold">
                  <Icon name="hardDrive" className="text-muted" />
                  {host}
                </h3>
                <ul className="mt-1 divide-y divide-line">
                  {drives.map((d) => (
                    <li key={d.id} className="py-2">
                      <DriveRow drive={d} onDelete={setPending} />
                    </li>
                  ))}
                </ul>
              </section>
            ))}
          </div>
        )}
      </Card>

      {pending?.kind === "drive" && (
        <DeleteDriveDialog
          drive={pending.drive}
          onClose={() => setPending(null)}
          onStarted={started}
        />
      )}
      {pending?.kind === "service" && (
        <DeleteServiceDialog
          account={pending.account}
          service={pending.service}
          onClose={() => setPending(null)}
          onStarted={started}
        />
      )}
      {pending?.kind === "account" && (
        <DisconnectDialog
          account={pending.account}
          onClose={() => setPending(null)}
          onStarted={started}
        />
      )}
    </div>
  );
}

function Deleting() {
  return (
    <span className="inline-flex items-center gap-2 text-sm text-muted">
      <Spinner />
      Deleting…
    </span>
  );
}

function AccountRow({
  account: a,
  onDelete,
}: {
  account: SettingsAccount;
  onDelete: (p: Pending) => void;
}) {
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
      <div className="min-w-0">
        <p className="font-medium wrap-anywhere">{a.label}</p>
        <dl className="mt-1 grid grid-cols-[max-content_1fr_auto] items-center gap-x-3 gap-y-1 text-sm">
          {(Object.keys(serviceNames) as RecordedService[]).map((s) => {
            const r = a.recorded[s];
            const [name, noun] = serviceNames[s];
            // Something to delete: records, or scans.
            const has = (r.files ?? 0) > 0 || (a.service_scans[s] ?? 0) > 0;
            return (
              <div key={s} className="contents">
                <dt className="text-muted">{name}</dt>
                <dd className="tabular-nums">
                  {r.files !== undefined
                    ? `${count(r.files, noun)} · ${formatBytes(r.bytes ?? 0)}`
                    : "—"}
                </dd>
                <dd>
                  {has && !a.job && (
                    <Button
                      variant="dangerOutline"
                      size="sm"
                      disabled={!!a.running_scan}
                      onClick={() =>
                        onDelete({ kind: "service", account: a, service: s })
                      }
                    >
                      Delete {name} data
                    </Button>
                  )}
                </dd>
              </div>
            );
          })}
        </dl>
        <p className="mt-1 text-xs text-muted">{count(a.scans, "scan")}</p>
        {a.running_scan ? (
          <p className="mt-1 text-xs text-warning">
            Scan {a.running_scan} is running; its data can be deleted once it
            ends.
          </p>
        ) : null}
      </div>
      <div className="flex shrink-0 flex-col gap-2 sm:flex-row">
        {a.job ? (
          <Deleting />
        ) : (
          <Button
            variant="dangerOutline"
            size="sm"
            disabled={!!a.running_scan}
            onClick={() => onDelete({ kind: "account", account: a })}
          >
            Disconnect account
          </Button>
        )}
      </div>
    </div>
  );
}

function DriveRow({
  drive: d,
  onDelete,
}: {
  drive: SettingsDrive;
  onDelete: (p: Pending) => void;
}) {
  return (
    <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
      <div className="min-w-0">
        <p className="font-medium">{d.drive_id}</p>
        <p className="text-sm text-muted tabular-nums">
          {count(d.files, "file")} · {formatBytes(d.bytes)}
          {d.last_synced_at
            ? ` · synced ${formatAgo(d.last_synced_at)}`
            : " · never synced"}
        </p>
        {d.other_copies.length > 0 && (
          <p className="text-xs text-muted">
            Also uploaded from {d.other_copies.join(", ")}
          </p>
        )}
      </div>
      <div className="shrink-0">
        {d.job ? (
          <Deleting />
        ) : (
          <Button
            variant="dangerOutline"
            size="sm"
            className="w-full sm:w-auto"
            onClick={() => onDelete({ kind: "drive", drive: d })}
          >
            Delete…
          </Button>
        )}
      </div>
    </div>
  );
}

// The confirm button of a dialog, and its error.
function useDeletion(
  start: () => Promise<DeletionJob>,
  onStarted: (job: DeletionJob) => void
) {
  return useMutation({ mutationFn: start, onSuccess: onStarted });
}

function ConfirmDialog({
  title,
  children,
  confirmLabel,
  onClose,
  onConfirm,
  busy,
  error,
  canConfirm = true,
}: {
  title: ReactNode;
  children: ReactNode;
  confirmLabel: string;
  onClose: () => void;
  onConfirm: () => void;
  busy: boolean;
  error: Error | null;
  canConfirm?: boolean;
}) {
  const cancel = useRef<HTMLButtonElement>(null);
  return (
    <Dialog
      title={title}
      onClose={onClose}
      initialFocus={cancel}
      actions={
        <>
          <Button ref={cancel} variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="danger"
            loading={busy}
            disabled={!canConfirm}
            onClick={onConfirm}
          >
            {confirmLabel}
          </Button>
        </>
      }
    >
      {children}
      {error && (
        <p role="alert" className="text-danger">
          {error.message}
        </p>
      )}
    </Dialog>
  );
}

function DeleteDriveDialog({
  drive: d,
  onClose,
  onStarted,
}: {
  drive: SettingsDrive;
  onClose: () => void;
  onStarted: (job: DeletionJob) => void;
}) {
  const del = useDeletion(() => deleteAgentDrive(d.id), onStarted);
  const host = d.hostname || "this machine";
  return (
    <ConfirmDialog
      title={`Delete ${d.drive_id} from ${host}?`}
      confirmLabel="Delete drive"
      onClose={onClose}
      onConfirm={() => del.mutate()}
      busy={del.isPending}
      error={del.error}
    >
      <p>
        This deletes what Bhandaar holds for this drive as uploaded from this
        box: {count(d.files, "file")} ({formatBytes(d.bytes)} recorded) and its
        scan history. Nothing on the drive is touched
        {d.other_copies.length > 0
          ? `, nor its copies uploaded from ${d.other_copies.join(", ")}`
          : ""}
        .
      </p>
      <p>
        If {host} scans or syncs {d.drive_id} again, it comes back: a scan
        re-creates it, and the next sync uploads all of it.{" "}
        <strong>This can&apos;t be undone.</strong>
      </p>
    </ConfirmDialog>
  );
}

function DeleteServiceDialog({
  account: a,
  service,
  onClose,
  onStarted,
}: {
  account: SettingsAccount;
  service: RecordedService;
  onClose: () => void;
  onStarted: (job: DeletionJob) => void;
}) {
  const del = useDeletion(
    () => deleteServiceData(a.client_key, service),
    onStarted
  );
  const [name, noun] = serviceNames[service];
  const records = a.recorded[service].files ?? 0;
  const scans = a.service_scans[service] ?? 0;
  const what = [
    records > 0 ? count(records, noun) : "",
    scans > 0 ? count(scans, `${name} scan`) : "",
  ]
    .filter(Boolean)
    .join(" and ");
  return (
    <ConfirmDialog
      title={`Delete ${name} data for ${a.label}?`}
      confirmLabel={`Delete ${name} data`}
      onClose={onClose}
      onConfirm={() => del.mutate()}
      busy={del.isPending}
      error={del.error}
    >
      <p>
        This deletes {what} from Bhandaar. Nothing in {name} is touched, and the
        account stays connected, so you can scan it again.{" "}
        <strong>This can&apos;t be undone.</strong>
      </p>
    </ConfirmDialog>
  );
}

function DisconnectDialog({
  account: a,
  onClose,
  onStarted,
}: {
  account: SettingsAccount;
  onClose: () => void;
  onStarted: (job: DeletionJob) => void;
}) {
  const [typed, setTyped] = useState("");
  const matches = typed.trim() === a.label;
  const del = useDeletion(
    () => disconnectAccount(a.client_key, typed.trim()),
    onStarted
  );
  const recorded = (Object.keys(serviceNames) as RecordedService[])
    .filter((s) => (a.recorded[s].files ?? 0) > 0)
    .map(
      (s) =>
        `${count(a.recorded[s].files ?? 0, serviceNames[s][1])} (${serviceNames[s][0]})`
    );

  function submit(e: FormEvent) {
    e.preventDefault();
    if (matches && !del.isPending) {
      del.mutate();
    }
  }

  return (
    <ConfirmDialog
      title={`Disconnect ${a.label}?`}
      confirmLabel="Disconnect account"
      onClose={onClose}
      onConfirm={() => del.mutate()}
      busy={del.isPending}
      error={del.error}
      canConfirm={matches}
    >
      <p>
        This revokes Bhandaar&apos;s access at Google and deletes everything
        Bhandaar has recorded for this account
        {recorded.length > 0
          ? `: ${recorded.join(", ")}, and all ${count(a.scans, "scan")}`
          : ` and its ${count(a.scans, "scan")}`}
        . Nothing in your Google account is touched.{" "}
        <strong>This can&apos;t be undone.</strong>
      </p>
      <form onSubmit={submit} className="space-y-1.5">
        <label htmlFor="confirm-name" className="block">
          To confirm, type{" "}
          <code className="rounded bg-surface-muted px-1 py-0.5 font-mono text-xs">
            {a.label}
          </code>{" "}
          below:
        </label>
        <Input
          id="confirm-name"
          autoComplete="off"
          spellCheck={false}
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          className="font-mono"
        />
      </form>
    </ConfirmDialog>
  );
}
