// OAuth `state` for Google account linking. It ties the callback to the tab
// that started the flow, so a link crafted elsewhere can't attach an account.
const OAUTH_STATE_KEY = "oauthState";

/** Creates a fresh `state` value and remembers it for this tab. */
export function createOAuthState(): string {
  const state = crypto.randomUUID();
  sessionStorage.setItem(OAUTH_STATE_KEY, state);
  return state;
}

/** True if `state` is the value this tab sent to Google. */
export function isExpectedOAuthState(state: string): boolean {
  const expected = sessionStorage.getItem(OAUTH_STATE_KEY);
  return expected !== null && state === expected;
}
