import { backend_url } from "../api";
import useSSE from "../components/hooks/useSse";
import { useState } from "react";
import { Progress } from "../types/scans";
import { formatDuration } from "../format";

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
      <div id="container" className="border-2 border-gray-200 gap-2">
        <table className="w-5/8 mt-3 text-sm text-left rtl:text-right text-gray-500 dark:text-gray-400 justify-self-center">
          <thead>
            <tr className="text-xs text-gray-700 uppercase bg-gray-50 dark:bg-gray-700 dark:text-gray-400">
              <th scope="col" className="px-6 py-3">
                Scan Id
              </th>
              <th scope="col" className="px-6 py-3">
                Elapsed
              </th>
              <th scope="col" className="px-6 py-3">
                Processed
              </th>
              <th scope="col" className="px-6 py-3">
                Processing
              </th>
              <th scope="col" className="px-6 py-3">
                Progress
              </th>
            </tr>
          </thead>
          <tbody className="">
            <tr
              key={sseData.scan_id}
              className="odd:bg-white odd:dark:bg-gray-900 even:bg-gray-50 even:dark:bg-gray-800 border-b dark:border-gray-700 border-gray-200"
            >
              <td className="px-6 py-4">{sseData.scan_id}</td>
              <td className="px-6 py-4">
                {formatDuration(sseData.elapsed_in_sec)}
              </td>
              <td className="px-6 py-4">{sseData.processed_count}</td>
              <td className="px-6 py-4">{sseData.active_count}</td>
              <td className="px-6 py-4">
                <ProgressBar progress={sseData} />
              </td>
            </tr>
          </tbody>
        </table>
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
