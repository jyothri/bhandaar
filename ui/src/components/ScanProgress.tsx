import { backend_url } from "../api";
import useSSE from "../components/hooks/useSse";
import { useState } from "react";
import { Progress } from "../types/scans";
import { formatDuration } from "../format";
import { Table, Td, Tr } from "./Table";

export default function ScanProgress() {
  const [sseData, setSseData] = useState<Progress | null>(null);

  const { error: sseError } = useSSE<Progress>(
    backend_url + "/sse/scanprogress",
    "progress",
    "close",
    setSseData
  );

  // Shown even before the first update, so a stream that fails right away
  // isn't silent.
  const errorLine = sseError && (
    <div className="text-red-500">Scan progress unavailable: {sseError}</div>
  );

  if (!sseData) {
    return errorLine || null;
  }

  return (
    <div>
      <h4 className="p-2 justify-self-center font-bold text-lg">
        Scan Progress
      </h4>
      <div id="container" className="border-2 border-gray-200 dark:border-gray-700 gap-2">
        <Table
          className="w-5/8"
          headers={[
            "Scan Id",
            "Elapsed",
            "Processed",
            "Processing",
            "Progress",
          ]}
        >
          <Tr key={sseData.scan_id}>
            <Td>{sseData.scan_id}</Td>
            <Td>{formatDuration(sseData.elapsed_in_sec)}</Td>
            <Td>{sseData.processed_count}</Td>
            <Td>{sseData.active_count}</Td>
            <Td>
              <ProgressBar progress={sseData} />
            </Td>
          </Tr>
        </Table>
        {errorLine}
      </div>
    </div>
  );
}

// The backend doesn't send completion_pct yet (review item 7.6), so the bar
// is indeterminate while messages are being fetched, and switches to a
// percentage once one arrives. ETA is hidden for the same reason.
function ProgressBar({ progress }: { progress: Progress }) {
  if (progress.completion_pct > 0) {
    return (
      <progress
        max={100}
        value={progress.completion_pct}
        aria-label={`${Math.round(progress.completion_pct)}% complete`}
      />
    );
  }
  if (progress.active_count > 0) {
    return <progress aria-label="Scan in progress" />;
  }
  return <span>—</span>;
}
