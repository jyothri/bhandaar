/** A service a linked account can be scanned for. */
export type Service = "gmail" | "drive" | "photos";

export type Account = {
  clientKey: string;
  displayName: string;
  // The services the account has granted access to.
  services: Service[];
  // The Google account ID, for Google's login_hint; missing for accounts
  // linked before it was recorded.
  loginHint?: string;
};

/** An account that has scans, as Request History lists it. */
export type ScannedAccount = {
  clientKey: string;
  displayName: string;
};
