// A scan's results, as its results view reads them.

export type ScanSummary = {
  scan_id: number;
  scan_type: string;
  name: string;
  // The linked account a Google scan ran as; empty for other scans.
  client_key: string;
  // A Drive folder scan's folder, e.g. "My Drive/A (1AbC…) and subfolders".
  search_path: string;
  search_filter: string;
  status: string;
  scan_start_time: string;
  // Seconds as a decimal string, or "-1" while the scan has no end time.
  scan_duration_in_sec: string;
  // Files, or for Gmail new messages, and their total size; folder rows
  // aren't counted.
  item_count: number;
  total_bytes: number;
  folder_count: number;
  // Google Photos items with no size; 0 for other scans.
  unsized_count: number;
};

export type PaginationInfo = {
  // The total number of rows, across pages.
  size: number;
  page: number;
};

/** A file or folder a scan found. */
export type ScanDataRow = {
  scan_data_id: number;
  name: string;
  path: string;
  size: number;
  modified: string | null;
  md5: string;
  is_dir: boolean;
  // For a folder, the files under it.
  file_count: number;
  // A cloud file's ID; null for local files.
  file_id: string | null;
};

export type ScanDataPage = {
  pagination_info: PaginationInfo;
  scan_data: ScanDataRow[];
};

/** A message a Gmail scan found. */
export type MessageRow = {
  message_metadata_id: number;
  from: string;
  to: string;
  subject: string;
  date: string | null;
  size_estimate: number;
};

export type MessagePage = {
  pagination_info: PaginationInfo;
  message_metadata: MessageRow[];
};
