import { createContext, useCallback, useContext, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  fetchAuthContext,
  fetchMe,
  loginPasskey,
  logout,
  passkeyAvailable,
  passwordLogin,
  redeemCode,
  registerPasskey,
  setPassword,
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
      } else if (me.setup_live) {
        // A redeemed link's setup session is still live in this browser —
        // resume the wizard (covers closing the tab mid-setup).
        setState({ phase: "setup" });
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
          // The link is spent — but maybe THIS browser spent it: already
          // logged in, or mid-setup with a live setup cookie. Reopening the
          // same link must continue, not dead-end on "invalid".
          const me = await fetchMe().catch(() => null);
          if (me?.authenticated) {
            setState({ phase: "ready", me });
          } else if (me?.setup_live) {
            setState({ phase: "setup" });
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
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  // null while the probe runs; the passkey section only renders on true, so
  // devices with no passkey provider see a clean password-only form.
  const [passkeyOk, setPasskeyOk] = useState<boolean | null>(null);
  useEffect(() => {
    void passkeyAvailable().then(setPasskeyOk);
  }, []);

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

  const signInPassword = async () => {
    setBusy(true);
    setFailure(null);
    try {
      await passwordLogin(username.trim(), password);
      onDone();
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const passwordReady = username.trim() !== "" && password !== "";

  return (
    <CenterCard>
      {linkInvalid ? (
        <p className="text-destructive mb-4 text-sm">
          This login link is invalid or has expired. Ask the operator for a
          fresh one (links are single-use, 30 minutes), or sign in below if
          this site already knows you.
        </p>
      ) : (
        <p className="text-muted-foreground mb-4 text-sm">
          Sign in with your username and password, or with a passkey. No
          account yet? Ask the operator for a login link.
        </p>
      )}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (passwordReady && !busy) void signInPassword();
        }}
        className="flex flex-col gap-2"
      >
        <Input
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          placeholder="username"
          autoComplete="username"
        />
        <Input
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          placeholder="password"
          autoComplete="current-password"
        />
        <Button type="submit" disabled={busy || !passwordReady} className="w-full">
          {busy ? "Signing in…" : "Sign in"}
        </Button>
      </form>
      {passkeyOk === true && (
        <>
          <div className="text-muted-foreground my-3 flex items-center gap-2 text-xs">
            <span className="bg-border h-px flex-1" />
            or
            <span className="bg-border h-px flex-1" />
          </div>
          <Button
            variant="outline"
            onClick={() => void signIn()}
            disabled={busy}
            className="w-full"
          >
            Sign in with passkey
          </Button>
        </>
      )}
      {passkeyOk === false && !window.isSecureContext ? (
        // Passkeys only exist in secure contexts — explain why the button is
        // gone for anyone who registered one elsewhere. Silent hiding is
        // reserved for devices that never had a passkey provider.
        <p className="text-muted-foreground mt-3 text-xs">
          Passkey sign-in is unavailable here: passkeys require HTTPS (or
          localhost), and this page was opened over plain HTTP at{" "}
          {window.location.host}. Password sign-in still works.
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
  // Credential-choice state for the final step: passkey (default) or password.
  const [usePassword, setUsePassword] = useState(false);
  const [pw, setPw] = useState("");
  const [pw2, setPw2] = useState("");
  const pwReady = pw.length >= 8 && pw === pw2;
  // Devices with no passkey provider skip the choice — password only.
  const [passkeyOk, setPasskeyOk] = useState<boolean | null>(null);
  useEffect(() => {
    void passkeyAvailable().then(setPasskeyOk);
  }, []);
  const passwordOnly = passkeyOk === false;

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

  const runSetPassword = async () => {
    setBusy(true);
    setFailure(null);
    try {
      await setPassword(pw);
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
            Last step: pick how{" "}
            <span className="text-foreground font-medium">{step.username}</span>{" "}
            will sign in from now on — the link you used is now spent.
          </p>
          {!usePassword && !passwordOnly ? (
            <>
              <Button
                disabled={busy}
                onClick={() => void runRegister()}
                className="w-full"
              >
                {busy ? "Waiting for passkey…" : "Register passkey"}
              </Button>
              <Button
                variant="ghost"
                disabled={busy}
                onClick={() => {
                  setFailure(null);
                  setUsePassword(true);
                }}
                className="mt-2 w-full"
              >
                Use a password instead
              </Button>
              <p className="text-muted-foreground mt-2 text-xs">
                No passkey prompt appearing? Some devices (e.g. Android
                without Google services) have no passkey support — use a
                password instead.
              </p>
            </>
          ) : (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                if (pwReady && !busy) void runSetPassword();
              }}
              className="flex flex-col gap-2"
            >
              <Input
                type="password"
                value={pw}
                onChange={(e) => setPw(e.target.value)}
                placeholder="password (min 8 characters)"
                autoComplete="new-password"
                autoFocus
              />
              <Input
                type="password"
                value={pw2}
                onChange={(e) => setPw2(e.target.value)}
                placeholder="repeat password"
                autoComplete="new-password"
              />
              {pw !== "" && pw.length < 8 && (
                <p className="text-muted-foreground text-xs">
                  At least 8 characters.
                </p>
              )}
              {pw2 !== "" && pw !== pw2 && (
                <p className="text-destructive text-xs">
                  Passwords don't match.
                </p>
              )}
              <Button type="submit" disabled={busy || !pwReady} className="w-full">
                {busy ? "Saving…" : "Set password & sign in"}
              </Button>
              {!passwordOnly && (
                <Button
                  variant="ghost"
                  disabled={busy}
                  onClick={() => {
                    setFailure(null);
                    setUsePassword(false);
                  }}
                  className="w-full"
                >
                  Back to passkey
                </Button>
              )}
              {passwordOnly && (
                <p className="text-muted-foreground text-xs">
                  This device has no passkey support — a password is how{" "}
                  {step.username} will sign in.
                </p>
              )}
            </form>
          )}
        </>
      )}

      {failure && <p className="text-destructive mt-3 text-xs">{failure}</p>}
    </CenterCard>
  );
}
