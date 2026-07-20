import { createContext, useCallback, useContext, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  fetchAuthContext,
  fetchMe,
  loginPasskey,
  logout,
  redeemCode,
  registerPasskey,
  setupPerson,
  type AuthMe,
  type ChannelIdentity,
  type PersonSummary,
} from "@/lib/auth-api";

// AuthContext exposes who is logged in (for the sidebar account chip) and a
// logout handler. me is null while loading or when the gate is not passed.
export const AuthContext = createContext<{
  me: AuthMe | null;
  signOut: () => void;
}>({ me: null, signOut: () => {} });

export function useAuth() {
  return useContext(AuthContext);
}

type GateState =
  | { phase: "loading" }
  | { phase: "ready"; me: AuthMe }
  | { phase: "login"; error?: string; linkInvalid?: boolean }
  | { phase: "setup" };

// AuthGate blocks the app until the browser is authenticated (or exempt).
// A ?login_code= URL enters the one-time setup flow: create or associate a
// person, then register a passkey.
export function AuthGate({ children }: { children: React.ReactNode }) {
  const [state, setState] = useState<GateState>({ phase: "loading" });

  const refresh = useCallback(async () => {
    try {
      const me = await fetchMe();
      if (me.authenticated) {
        setState({ phase: "ready", me });
      } else {
        setState({ phase: "login" });
      }
    } catch (e) {
      setState({ phase: "login", error: String(e) });
    }
  }, []);

  useEffect(() => {
    const url = new URL(window.location.href);
    const code = url.searchParams.get("login_code");
    if (!code) {
      void refresh();
      return;
    }
    // Strip the code from the address bar immediately — it is single-use
    // and must not linger in history/bookmarks.
    url.searchParams.delete("login_code");
    window.history.replaceState(null, "", url.toString());
    redeemCode(code)
      .then(async (ok) => {
        if (!ok) {
          // Maybe this browser is already logged in and the link is stale.
          const me = await fetchMe().catch(() => null);
          if (me?.authenticated) {
            setState({ phase: "ready", me });
          } else {
            setState({ phase: "login", linkInvalid: true });
          }
          return;
        }
        setState({ phase: "setup" });
      })
      .catch((e: unknown) => setState({ phase: "login", error: String(e) }));
  }, [refresh]);

  const signOut = useCallback(() => {
    void logout().finally(() => {
      setState({ phase: "login" });
    });
  }, []);

  if (state.phase === "loading") {
    return (
      <div className="text-muted-foreground flex h-dvh items-center justify-center text-sm">
        Loading…
      </div>
    );
  }
  if (state.phase === "login") {
    return (
      <LoginView
        linkInvalid={state.linkInvalid}
        error={state.error}
        onDone={refresh}
      />
    );
  }
  if (state.phase === "setup") {
    return <SetupWizard onDone={refresh} />;
  }
  return (
    <AuthContext.Provider value={{ me: state.me, signOut }}>
      {children}
    </AuthContext.Provider>
  );
}

function CenterCard({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-dvh items-center justify-center p-4">
      <div className="w-full max-w-md rounded-lg border p-6 shadow-sm">
        <h1 className="mb-1 text-lg font-semibold">nagobot</h1>
        {children}
      </div>
    </div>
  );
}

function LoginView({
  linkInvalid,
  error,
  onDone,
}: {
  linkInvalid?: boolean;
  error?: string;
  onDone: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(error ?? null);
  // Passkeys only exist in secure contexts: Safari/Chrome hide the WebAuthn
  // API entirely on plain-http origins (anything but localhost), and an IP
  // address can never be a WebAuthn RP ID. Surface the real cause instead of
  // the library's misleading "not supported in this browser".
  const insecure = !window.isSecureContext;

  const signIn = async () => {
    setBusy(true);
    setFailure(null);
    try {
      await loginPasskey();
      onDone();
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <CenterCard>
      {linkInvalid ? (
        <p className="text-destructive mb-4 text-sm">
          This login link is invalid or has expired. Ask the operator for a
          fresh one (links are single-use, 30 minutes), or sign in with a
          passkey if this browser has one.
        </p>
      ) : (
        <p className="text-muted-foreground mb-4 text-sm">
          Sign in with the passkey registered for this site. No passkey? Ask
          the operator for a login link.
        </p>
      )}
      <Button
        onClick={() => void signIn()}
        disabled={busy || insecure}
        className="w-full"
      >
        {busy ? "Waiting for passkey…" : "Sign in with passkey"}
      </Button>
      {insecure ? (
        <p className="text-destructive mt-3 text-xs">
          Passkeys require a secure context (HTTPS, or localhost). This page
          was opened over plain HTTP at {window.location.host}, where the
          browser disables WebAuthn — open the site via an HTTPS hostname
          (e.g. tailscale serve) to sign in from this device.
        </p>
      ) : null}
      {failure && <p className="text-destructive mt-3 text-xs">{failure}</p>}
    </CenterCard>
  );
}

type WizardBase =
  | { kind: "new" }
  | { kind: "person"; person: PersonSummary }
  | { kind: "identity"; identity: ChannelIdentity };

type WizardStep =
  | { step: "choose" }
  | { step: "username" }
  | { step: "confirm" }
  | { step: "passkey"; username: string };

// SetupWizard is the one-time flow behind a redeemed login link.
//
// "Existing user" deliberately means BOTH web accounts and channel
// identities: someone who has talked to the bot on Discord already IS a
// user here. Claiming a channel identity creates the web account around it
// (username prefilled from the channel display name); picking a web account
// is the lost-passkey recovery path. One login = one person, exactly:
// claiming binds only the identity that was picked — merging further
// identities into a person is a separate mechanism, not this wizard.
function SetupWizard({ onDone }: { onDone: () => void }) {
  const [persons, setPersons] = useState<PersonSummary[]>([]);
  const [identities, setIdentities] = useState<ChannelIdentity[]>([]);
  const [step, setStep] = useState<WizardStep>({ step: "choose" });
  const [base, setBase] = useState<WizardBase>({ kind: "new" });
  const [username, setUsername] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    fetchAuthContext()
      .then((ctx) => {
        setPersons(ctx.persons);
        setIdentities(ctx.identities);
      })
      .catch((e: unknown) => setFailure(String(e)));
  }, []);

  const boundIdentityKeys = new Set(persons.flatMap((p) => p.identities ?? []));
  const unboundIdentities = identities.filter((id) => !boundIdentityKeys.has(id.key));
  const displayName =
    base.kind === "person" ? base.person.username : username.trim();

  const startFrom = (b: WizardBase) => {
    setBase(b);
    setFailure(null);
    if (b.kind === "person") {
      setStep({ step: "confirm" });
      return;
    }
    setUsername(b.kind === "identity" ? b.identity.name : "");
    setStep({ step: "username" });
  };

  // The one identity this login binds — the claimed one, or nothing.
  const claimed = base.kind === "identity" ? [base.identity.key] : [];

  const submitSetup = async () => {
    setBusy(true);
    setFailure(null);
    try {
      const resp = await setupPerson(
        base.kind === "person"
          ? { mode: "associate", person_id: base.person.id }
          : { mode: "create", username: displayName, identities: claimed },
      );
      setStep({ step: "passkey", username: resp.username });
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e));
      setStep(base.kind === "person" ? { step: "choose" } : { step: "username" });
    } finally {
      setBusy(false);
    }
  };

  const runRegister = async () => {
    setBusy(true);
    setFailure(null);
    try {
      await registerPasskey();
      onDone();
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <CenterCard>
      {step.step === "choose" && (
        <>
          <p className="text-muted-foreground mb-3 text-sm">
            Who are you? Pick yourself below, or start fresh.
          </p>
          <div className="flex max-h-72 flex-col gap-1 overflow-y-auto">
            {persons.map((p) => (
              <button
                key={p.id}
                type="button"
                onClick={() => startFrom({ kind: "person", person: p })}
                className="border-border hover:bg-muted rounded-md border px-3 py-2 text-start text-sm"
              >
                <span className="font-medium">{p.username}</span>
                <span className="text-muted-foreground ms-2 text-xs">
                  web account
                  {(p.identities ?? []).length > 0 &&
                    ` · ${(p.identities ?? []).join(", ")}`}
                </span>
              </button>
            ))}
            {unboundIdentities.map((id) => (
              <button
                key={id.key}
                type="button"
                onClick={() => startFrom({ kind: "identity", identity: id })}
                className="border-border hover:bg-muted rounded-md border px-3 py-2 text-start text-sm"
              >
                <span className="font-medium">{id.name}</span>
                <span className="text-muted-foreground ms-2 font-mono text-xs">{id.key}</span>
              </button>
            ))}
            {persons.length === 0 && unboundIdentities.length === 0 && (
              <p className="text-muted-foreground text-xs">
                Nobody known yet — you are the first.
              </p>
            )}
          </div>
          <Button className="mt-3 w-full" onClick={() => startFrom({ kind: "new" })}>
            None of these — create a new user
          </Button>
        </>
      )}

      {step.step === "username" && (
        <>
          <p className="text-muted-foreground mb-3 text-sm">
            {base.kind === "identity" ? (
              <>
                Claiming{" "}
                <span className="text-foreground font-medium">{base.identity.name}</span>{" "}
                <span className="font-mono text-xs">({base.identity.key})</span>. Pick a
                username for the web account.
              </>
            ) : (
              <>Pick a username.</>
            )}
          </p>
          <Input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            placeholder="username"
            autoFocus
          />
          <div className="mt-3 flex gap-2">
            <Button
              disabled={username.trim() === ""}
              onClick={() => setStep({ step: "confirm" })}
            >
              Continue
            </Button>
            <Button variant="ghost" onClick={() => setStep({ step: "choose" })}>
              Back
            </Button>
          </div>
        </>
      )}

      {step.step === "confirm" && (
        <>
          <p className="mb-3 text-sm">
            {base.kind === "person" ? (
              <>
                Sign in as <span className="font-medium">{displayName}</span>
              </>
            ) : (
              <>
                Create user <span className="font-medium">{displayName}</span>
              </>
            )}
            {claimed.length > 0 && (
              <>
                {" "}
                as <span className="font-mono text-xs">{claimed.join(", ")}</span>
              </>
            )}
            ?
          </p>
          <div className="flex gap-2">
            <Button disabled={busy} onClick={() => void submitSetup()}>
              {busy ? "Saving…" : "Confirm"}
            </Button>
            <Button
              variant="ghost"
              onClick={() =>
                setStep(base.kind === "person" ? { step: "choose" } : { step: "username" })
              }
            >
              Back
            </Button>
          </div>
        </>
      )}

      {step.step === "passkey" && (
        <>
          <p className="text-muted-foreground mb-4 text-sm">
            Last step: register a passkey for{" "}
            <span className="text-foreground font-medium">{step.username}</span>
            . It becomes the login for this site — the link you used is now
            spent.
          </p>
          <Button disabled={busy} onClick={() => void runRegister()} className="w-full">
            {busy ? "Waiting for passkey…" : "Register passkey"}
          </Button>
        </>
      )}

      {failure && <p className="text-destructive mt-3 text-xs">{failure}</p>}
    </CenterCard>
  );
}
