// Sends the user to Google to link an account for one service. See
// docs/archive/request-drive-scans.md, "Scopes".
import { config } from "./config";
import { createOAuthState, rememberLinkService } from "./oauthState";
import { Service } from "./types/accounts";

const AUTH_URL = "https://accounts.google.com/o/oauth2/v2/auth";

/** The Google scopes each service needs, space-separated. */
export const serviceScopes: Record<Service, string> = {
  gmail: "https://www.googleapis.com/auth/gmail.readonly",
  // Metadata only: the scan never reads file contents.
  drive: "https://www.googleapis.com/auth/drive.metadata.readonly",
  // Only what the user picks; see docs/archive/photos-picker.md.
  photos: "https://www.googleapis.com/auth/photospicker.mediaitems.readonly",
  // Read-only buckets and objects, and the account's project list; see
  // docs/archive/gcs-scans.md.
  gcs:
    "https://www.googleapis.com/auth/devstorage.read_only " +
    "https://www.googleapis.com/auth/cloudplatformprojects.readonly",
};

/**
 * Links an account for service, or adds service to the account loginHint
 * (a Google account ID) names.
 */
export function linkGoogleAccount(service: Service, loginHint?: string) {
  const params = new URLSearchParams({
    response_type: "code",
    // openid email identifies the account, whatever else is granted.
    scope: `openid email ${serviceScopes[service]}`,
    // Keep the account's earlier grants in the new token.
    include_granted_scopes: "true",
    client_id: config.googleClientId,
    state: createOAuthState(),
    redirect_uri: `${window.location.origin}/oauth/glink`,
    access_type: "offline",
    prompt: "consent",
  });
  if (loginHint) {
    params.set("login_hint", loginHint);
  }
  rememberLinkService(service);
  window.location.href = `${AUTH_URL}?${params}`;
}
