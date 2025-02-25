import { ScanMetadata } from "../types/scans";

export const backend_url = "http://localhost:8090";

/**
 * Function to submit scan request.
 */
export const requestScan = async (scanData: ScanMetadata): Promise<void> => {
  const response = await fetch(backend_url + "/api/scans", {
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
    },
    body: JSON.stringify(scanData),
  });
  const content = await response.json();
  console.log("response from posting content", content);
  if (content.error != null) {
    throw new Error(content);
  }
  return content;
};
