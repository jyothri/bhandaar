/**
 * Where to go after logging in: `target` if it's a path on this site, else
 * "/". Rejects "//host/x" and absolute URLs, so the login page can't be used
 * to send someone elsewhere.
 */
export function safeRedirect(target: string): string {
  return target.startsWith("/") && !target.startsWith("//") ? target : "/";
}
