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
