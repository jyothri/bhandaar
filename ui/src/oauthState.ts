import { Service } from "./types/accounts";

// OAuth `state` for Google account linking. It ties the callback to the tab
// that started the flow, so a link crafted elsewhere can't attach an account.
const OAUTH_STATE_KEY = "oauthState";

/** Creates a fresh `state` value and remembers it for this tab. */
export function createOAuthState(): string {
  const state = crypto.randomUUID();
  sessionStorage.setItem(OAUTH_STATE_KEY, state);
  return state;
}

/**
 * True if `state` is the value this tab sent to Google. The stored value is
 * cleared either way, so each one can be used only once.
 */
export function consumeOAuthState(state: string): boolean {
  const expected = sessionStorage.getItem(OAUTH_STATE_KEY);
  sessionStorage.removeItem(OAUTH_STATE_KEY);
  return expected !== null && state === expected;
}

// The service being linked, so the Request page can come back to it: Google
// returns to /oauth/glink, the backend to /request?account=…, and neither
// carries it.
const LINK_SERVICE_KEY = "oauthLinkService";

/** Remembers, for this tab, the service a link is being started for. */
export function rememberLinkService(service: Service): void {
  sessionStorage.setItem(LINK_SERVICE_KEY, service);
}

/** The service the last link was started for, if any; it stays stored. */
export function linkService(): Service | null {
  const service = sessionStorage.getItem(LINK_SERVICE_KEY);
  return service === "gmail" || service === "drive" ? service : null;
}

/** Forgets the service the last link was started for. */
export function clearLinkService(): void {
  sessionStorage.removeItem(LINK_SERVICE_KEY);
}
