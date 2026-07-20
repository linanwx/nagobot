// Web-auth API client. The credential model: a one-time login link
// bootstraps the browser (create/associate a person + register a passkey);
// the passkey is the durable login. Cookies are HttpOnly — all state
// questions go through /api/auth/me.
import {
  startAuthentication,
  startRegistration,
} from "@simplewebauthn/browser";

export type AuthMe = {
  auth_enabled: boolean;
  exempt: boolean;
  authenticated: boolean;
  person_id?: string;
  username?: string;
  identities?: string[];
};

export type PersonSummary = {
  id: string;
  username: string;
  identities?: string[];
};

export type ChannelIdentity = {
  key: string; // "discord:1480..."
  name: string; // latest display name
  last_seen: string;
};

async function jsonOrThrow<T>(res: Response): Promise<T> {
  if (!res.ok) {
    const body = await res.text();
    throw new Error(body.trim() || `${res.status}`);
  }
  return (await res.json()) as T;
}

export async function fetchMe(): Promise<AuthMe> {
  return jsonOrThrow(await fetch("/api/auth/me"));
}

// redeemCode consumes a login-link code. Returns false when the link is
// invalid/expired/used (HTTP 410) — any other failure throws.
export async function redeemCode(code: string): Promise<boolean> {
  const res = await fetch("/api/auth/redeem", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code }),
  });
  if (res.status === 410) return false;
  await jsonOrThrow(res);
  return true;
}

export async function fetchAuthContext(): Promise<{
  persons: PersonSummary[];
  identities: ChannelIdentity[];
}> {
  return jsonOrThrow(await fetch("/api/auth/context"));
}

export async function setupPerson(req: {
  mode: "create" | "associate";
  username?: string;
  person_id?: string;
  identities?: string[];
}): Promise<{ person_id: string; username: string }> {
  return jsonOrThrow(
    await fetch("/api/auth/setup", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
    }),
  );
}

// registerPasskey runs the full WebAuthn registration ceremony against the
// live setup session and leaves the browser logged in (session cookie).
export async function registerPasskey(): Promise<{ username: string }> {
  const beginRes = await fetch("/api/auth/passkey/register/begin", {
    method: "POST",
  });
  const options = await jsonOrThrow<{ publicKey: never }>(beginRes);
  const attestation = await startRegistration({ optionsJSON: options.publicKey });
  return jsonOrThrow(
    await fetch("/api/auth/passkey/register/finish", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(attestation),
    }),
  );
}

// loginPasskey runs the usernameless assertion ceremony and leaves the
// browser logged in on success.
export async function loginPasskey(): Promise<{ username: string }> {
  const beginRes = await fetch("/api/auth/passkey/login/begin", {
    method: "POST",
  });
  const { flow_id, options } = await jsonOrThrow<{
    flow_id: string;
    options: { publicKey: never };
  }>(beginRes);
  const assertion = await startAuthentication({ optionsJSON: options.publicKey });
  return jsonOrThrow(
    await fetch(`/api/auth/passkey/login/finish?flow=${encodeURIComponent(flow_id)}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(assertion),
    }),
  );
}

export async function logout(): Promise<void> {
  await fetch("/api/auth/logout", { method: "POST" });
}
