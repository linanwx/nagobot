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
import { cn } from "@/lib/utils";

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
      <Button onClick={() => void signIn()} disabled={busy} className="w-full">
        {busy ? "Waiting for passkey…" : "Sign in with passkey"}
      </Button>
      {failure && <p className="text-destructive mt-3 text-xs">{failure}</p>}
    </CenterCard>
  );
}

type WizardStep =
  | { step: "choose" }
  | { step: "create" }
  | { step: "associate" }
  | { step: "identities"; personLabel: string; mode: "create" | "associate"; username?: string; personId?: string }
  | { step: "confirm"; mode: "create" | "associate"; username?: string; personId?: string; personLabel: string; identities: string[] }
  | { step: "passkey"; username: string };

// SetupWizard is the one-time flow behind a redeemed login link.
function SetupWizard({ onDone }: { onDone: () => void }) {
  const [persons, setPersons] = useState<PersonSummary[]>([]);
  const [identities, setIdentities] = useState<ChannelIdentity[]>([]);
  const [step, setStep] = useState<WizardStep>({ step: "choose" });
  const [username, setUsername] = useState("");
  const [personId, setPersonId] = useState<string | null>(null);
  const [picked, setPicked] = useState<Set<string>>(new Set());
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

  const submitSetup = async (s: Extract<WizardStep, { step: "confirm" }>) => {
    setBusy(true);
    setFailure(null);
    try {
      const resp = await setupPerson({
        mode: s.mode,
        username: s.username,
        person_id: s.personId,
        identities: s.identities,
      });
      setStep({ step: "passkey", username: resp.username });
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e));
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
          <p className="text-muted-foreground mb-4 text-sm">
            This login link lets you set up access for this browser. Are you
            new here, or claiming an existing user?
          </p>
          <div className="flex flex-col gap-2">
            <Button onClick={() => setStep({ step: "create" })}>
              Create a new user
            </Button>
            <Button
              variant="outline"
              onClick={() => setStep({ step: "associate" })}
              disabled={persons.length === 0}
            >
              I am an existing user
            </Button>
            {persons.length === 0 && (
              <p className="text-muted-foreground text-xs">
                No users exist yet — create the first one.
              </p>
            )}
          </div>
        </>
      )}

      {step.step === "create" && (
        <>
          <p className="text-muted-foreground mb-3 text-sm">Pick a username.</p>
          <Input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            placeholder="username"
            autoFocus
          />
          <div className="mt-3 flex gap-2">
            <Button
              disabled={username.trim() === ""}
              onClick={() =>
                setStep({
                  step: "identities",
                  mode: "create",
                  username: username.trim(),
                  personLabel: username.trim(),
                })
              }
            >
              Continue
            </Button>
            <Button variant="ghost" onClick={() => setStep({ step: "choose" })}>
              Back
            </Button>
          </div>
        </>
      )}

      {step.step === "associate" && (
        <>
          <p className="text-muted-foreground mb-3 text-sm">
            Which user are you?
          </p>
          <div className="flex max-h-64 flex-col gap-1 overflow-y-auto">
            {persons.map((p) => (
              <button
                key={p.id}
                type="button"
                onClick={() => setPersonId(p.id)}
                className={cn(
                  "rounded-md border px-3 py-2 text-start text-sm",
                  personId === p.id
                    ? "border-primary bg-primary/10"
                    : "border-border hover:bg-muted",
                )}
              >
                <span className="font-medium">{p.username}</span>
                {(p.identities ?? []).length > 0 && (
                  <span className="text-muted-foreground ms-2 text-xs">
                    {(p.identities ?? []).join(", ")}
                  </span>
                )}
              </button>
            ))}
          </div>
          <div className="mt-3 flex gap-2">
            <Button
              disabled={personId == null}
              onClick={() => {
                const p = persons.find((x) => x.id === personId);
                if (!p) return;
                setStep({
                  step: "identities",
                  mode: "associate",
                  personId: p.id,
                  personLabel: p.username,
                });
              }}
            >
              Continue
            </Button>
            <Button variant="ghost" onClick={() => setStep({ step: "choose" })}>
              Back
            </Button>
          </div>
        </>
      )}

      {step.step === "identities" && (
        <>
          <p className="text-muted-foreground mb-3 text-sm">
            Optionally link chat identities to{" "}
            <span className="text-foreground font-medium">{step.personLabel}</span>
            . Only pick identities that are actually you.
          </p>
          <div className="flex max-h-64 flex-col gap-1 overflow-y-auto">
            {identities.filter((id) => !boundIdentityKeys.has(id.key)).map((id) => (
              <label
                key={id.key}
                className="hover:bg-muted flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm"
              >
                <input
                  type="checkbox"
                  checked={picked.has(id.key)}
                  onChange={(e) => {
                    const next = new Set(picked);
                    if (e.target.checked) next.add(id.key);
                    else next.delete(id.key);
                    setPicked(next);
                  }}
                />
                <span className="font-medium">{id.name}</span>
                <span className="text-muted-foreground font-mono text-xs">{id.key}</span>
              </label>
            ))}
            {identities.filter((id) => !boundIdentityKeys.has(id.key)).length === 0 && (
              <p className="text-muted-foreground text-xs">
                No unbound chat identities known yet — you can skip this.
              </p>
            )}
          </div>
          <div className="mt-3 flex gap-2">
            <Button
              onClick={() =>
                setStep({
                  step: "confirm",
                  mode: step.mode,
                  username: step.username,
                  personId: step.personId,
                  personLabel: step.personLabel,
                  identities: [...picked],
                })
              }
            >
              Continue
            </Button>
            <Button
              variant="ghost"
              onClick={() =>
                setStep(step.mode === "create" ? { step: "create" } : { step: "associate" })
              }
            >
              Back
            </Button>
          </div>
        </>
      )}

      {step.step === "confirm" && (
        <>
          <p className="mb-3 text-sm">
            {step.mode === "create" ? (
              <>
                Create user{" "}
                <span className="font-medium">{step.personLabel}</span>
              </>
            ) : (
              <>
                Sign in as{" "}
                <span className="font-medium">{step.personLabel}</span>
              </>
            )}
            {step.identities.length > 0 && (
              <>
                {" "}
                and link:{" "}
                <span className="font-mono text-xs">
                  {step.identities.join(", ")}
                </span>
              </>
            )}
            ?
          </p>
          <div className="flex gap-2">
            <Button disabled={busy} onClick={() => void submitSetup(step)}>
              {busy ? "Saving…" : "Confirm"}
            </Button>
            <Button
              variant="ghost"
              onClick={() =>
                setStep({
                  step: "identities",
                  mode: step.mode,
                  username: step.username,
                  personId: step.personId,
                  personLabel: step.personLabel,
                })
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
