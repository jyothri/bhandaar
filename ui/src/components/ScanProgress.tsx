import { backend_url } from "../api";
import useSSE from "../components/hooks/useSse";
import { useState } from "react";
import { Progress } from "../types/scans";

export default function ScanProgress() {
  const [sseData, setSseData] = useState<Progress>({} as Progress);

  const setData: (arg0: Progress) => void = (scansProgress: Progress) => {
    setSseData(scansProgress);
  };

  const { error: sseError } = useSSE(
    backend_url + "/sse/scanprogress",
    "progress",
    "close",
    setData
  );

  return (
    sseData.scan_id && (
      <div>
        <h4 className="p-2 justify-self-center font-bold text-lg">
          Scan Progress
        </h4>
        <div id="container" className="border-2 border-gray-200 gap-2">
          <table className="w-5/8 mt-3 text-sm text-left rtl:text-right text-gray-500 dark:text-gray-400 justify-self-center">
            <thead>
              <tr className="text-xs text-gray-700 uppercase bg-gray-50 dark:bg-gray-700 dark:text-gray-400">
                <td scope="col" className="px-6 py-3">
                  Scan Id
                </td>
                <td scope="col" className="px-6 py-3">
                  Elapsted time (sec)
                </td>
                <td scope="col" className="px-6 py-3">
                  Processed
                </td>
                <td scope="col" className="px-6 py-3">
                  Processing
                </td>
                <td scope="col" className="px-6 py-3">
                  ETA
                </td>
              </tr>
            </thead>
            <tbody className="">
              <tr
                key={sseData.scan_id}
                className="odd:bg-white odd:dark:bg-gray-900 even:bg-gray-50 even:dark:bg-gray-800 border-b dark:border-gray-700 border-gray-200"
              >
                <td className="px-6 py-4">{sseData.scan_id}</td>
                <td className="px-6 py-4">{sseData.elapsed_in_sec}</td>
                <td className="px-6 py-4">{sseData.processed_count}</td>
                <td className="px-6 py-4">{sseData.active_count}</td>
                <td className="px-6 py-4">{sseData.eta_in_sec}</td>
              </tr>
            </tbody>
          </table>
          {sseError && <div>sseError: {sseError}</div>}
        </div>
      </div>
    )
  );
}
