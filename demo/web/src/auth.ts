// OpenID Connect login (authorization code + PKCE) against any standards
// compliant issuer: Keycloak locally, Cognito on AWS.
import { User, UserManager, WebStorageStateStore } from "oidc-client-ts";
import { config } from "./config";

export const userManager = new UserManager({
  authority: config.issuer,
  client_id: config.clientId,
  redirect_uri: window.location.origin + "/",
  post_logout_redirect_uri: window.location.origin + "/",
  response_type: "code",
  scope: "openid",
  userStore: new WebStorageStateStore({ store: window.sessionStorage }),
  automaticSilentRenew: true,
});

export interface Session {
  user: User;
  subject: string;
  name: string;
  roles: string[];
}

let restoring: Promise<Session | null> | null = null;

/**
 * Completes a login redirect if the URL carries one, then returns the session.
 * Memoised: an authorization code can be redeemed only once, and React's
 * strict mode runs effects twice in development.
 */
export function restoreSession(): Promise<Session | null> {
  restoring ??= doRestore();
  return restoring;
}

async function doRestore(): Promise<Session | null> {
  const params = new URLSearchParams(window.location.search);
  if (params.has("code") && params.has("state")) {
    await userManager.signinRedirectCallback();
    window.history.replaceState({}, "", window.location.pathname + window.location.hash);
  }
  const user = await userManager.getUser();
  if (!user || user.expired) return null;
  return toSession(user);
}

export function toSession(user: User): Session {
  const claims = decodeJwt(user.access_token);
  const roles = Array.isArray(claims.roles) ? (claims.roles as string[]) : [];
  const profile = user.profile;
  const name =
    (profile.name as string | undefined) ||
    (profile.preferred_username as string | undefined) ||
    String(claims.sub ?? "");
  return { user, subject: String(claims.sub ?? ""), name, roles };
}

function decodeJwt(token: string): Record<string, unknown> {
  try {
    const part = token.split(".")[1].replace(/-/g, "+").replace(/_/g, "/");
    const bytes = Uint8Array.from(atob(part), (c) => c.charCodeAt(0));
    return JSON.parse(new TextDecoder().decode(bytes));
  } catch {
    return {};
  }
}
