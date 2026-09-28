import { backend_url } from "../api";
import useSSE from "../components/hooks/useSse";
import { useState } from "react";
import { Progress } from "../types/scans";
import { formatDuration } from "../format";
import { describeScanError, mergeProgress } from "../progress";
import Badge from "./ui/Badge";
import Card from "./ui/Card";
import Icon from "./ui/Icon";
import Spinner from "./ui/Spinner";

export default function ScanProgress() {
  const [sseData, setSseData] = useState<Progress | null>(null);

  const { error: sseError } = useSSE<Progress>(
    backend_url + "/sse/scanprogress",
    "progress",
    "close",
    (next) => setSseData((current) => mergeProgress(current, next))
  );

  // Shown even before the first update, so a stream that fails right away
  // isn't silent.
  const errorLine = sseError && (
    <p className="text-sm text-danger">Scan progress unavailable: {sseError}</p>
  );

  if (!sseData) {
    return errorLine || null;
  }

  const stats: [string, string | number][] = [
    ["Elapsed", formatDuration(sseData.elapsed_in_sec)],
    ["Processed", sseData.processed_count],
    ["Processing", sseData.active_count],
  ];
  return (
    <Card
      title={`Scan ${sseData.scan_id}`}
      actions={<StatusBadge progress={sseData} />}
    >
      <div role="status" className="grid gap-4">
        <Outcome progress={sseData} />
        <dl className="grid grid-cols-3 gap-4">
          {stats.map(([label, value]) => (
            <div key={label}>
              <dt className="text-xs text-muted">{label}</dt>
              <dd className="text-lg font-semibold tabular-nums">{value}</dd>
            </div>
          ))}
        </dl>
      </div>
      {errorLine}
    </Card>
  );
}

function StatusBadge({ progress }: { progress: Progress }) {
  if (progress.status === "Completed") {
    return <Badge tone="success">Completed</Badge>;
  }
  if (progress.status === "Failed") {
    return <Badge tone="danger">Failed</Badge>;
  }
  return (
    <Badge tone="warning">
      <Spinner size={12} />
      Running
    </Badge>
  );
}

const bar = "h-2 w-full accent-accent-solid";

// Once the scan ends, its outcome replaces the bar. While it runs, the
// backend doesn't send completion_pct yet (review item 7.6), so the bar is
// indeterminate, and switches to a percentage once one arrives. ETA is
// hidden for the same reason.
function Outcome({ progress }: { progress: Progress }) {
  if (progress.status === "Completed") {
    return (
      <p className="flex items-center gap-2 text-sm text-success">
        <Icon name="check" />
        Completed
      </p>
    );
  }
  if (progress.status === "Failed") {
    return (
      <p
        className="flex items-start gap-2 text-sm text-danger wrap-anywhere"
        title={progress.error}
      >
        <Icon name="warning" className="mt-0.5 shrink-0" />
        Failed: {describeScanError(progress.error)}
      </p>
    );
  }
  if (progress.completion_pct > 0) {
    return (
      <progress
        max={100}
        value={progress.completion_pct}
        aria-label={`${Math.round(progress.completion_pct)}% complete`}
        className={bar}
      />
    );
  }
  if (progress.status === "Running" || progress.active_count > 0) {
    return <progress aria-label="Scan in progress" className={bar} />;
  }
  return <span className="text-muted">—</span>;
}
