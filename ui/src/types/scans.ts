export interface GMailScan {
  Filter: string;
  ClientKey: string;
  RefreshToken: string;
  Username: string;
}

export enum ScanType {
  Local = "Local",
  GDrive = "GDrive",
  GStorage = "GStorage",
  GMail = "GMail",
  GPhotos = "GPhotos",
}

export type ScanMetadata = {
  ScanType?: ScanType;
  GMailScan?: GMailScan;
};

export type RequestScanResponse = {
  scan_id: number;
};

export type Progress = {
  client_key: string;
  processed_count: number;
  active_count: number;
  completion_pct: number;
  elapsed_in_sec: number;
  eta_in_sec: number;
  scan_id: number;
};

export type ScanRequest = {
  scan_id: number;
  name: string;
  scan_type: string;
  search_filter: string;
  scan_start_time: string;
  // Seconds as a decimal string, or "-1" while the scan has no end time.
  scan_duration_in_sec: string;
  // "Running" until the scan ends, then "Completed" or "Failed" (also for an
  // interrupted scan). Scans from before statuses were recorded read
  // "Completed", even if they never finished.
  status: string;
};
