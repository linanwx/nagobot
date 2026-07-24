import { Component, type ErrorInfo, type ReactNode } from "react";

// ErrorBoundary is the app's last resort. Without one, any throw during render
// unmounts the entire React tree and leaves a blank white page — on a phone,
// with no console in reach, that is indistinguishable from a dead server.
//
// It deliberately SHOWS the error rather than swallowing it: a white screen and
// a silently-caught exception are the same non-diagnosis. The message and stack
// stay on screen so a failure can be reported from the device that hit it.
//
// Kept dependency-free on purpose (no ui/ components, no i18n): it renders in a
// tree that just proved it can crash, so anything it imports is another way to
// fail while reporting a failure.
type Props = { children: ReactNode };
type State = { error: Error | null; stack: string };

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null, stack: "" };

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    // Also emit to the console so remote debugging sees the component stack,
    // which the on-screen report omits for length.
    console.error("[nagobot] uncaught render error", error, info);
    this.setState({ stack: info.componentStack ?? "" });
  }

  private reload = (): void => {
    window.location.reload();
  };

  render(): ReactNode {
    const { error, stack } = this.state;
    if (!error) return this.props.children;

    return (
      <div className="fixed inset-0 overflow-auto bg-background p-6 text-foreground">
        <div className="mx-auto flex max-w-2xl flex-col gap-4">
          <h1 className="text-lg font-semibold">Something broke</h1>
          <p className="text-sm text-muted-foreground">
            The interface hit an unrecoverable error. The details below are the
            actual failure — include them in a bug report.
          </p>
          <pre className="overflow-x-auto rounded-md bg-muted p-3 text-xs whitespace-pre-wrap">
            {error.name}: {error.message}
            {error.stack ? `\n\n${error.stack}` : ""}
            {stack ? `\n\nComponent stack:${stack}` : ""}
          </pre>
          <button
            type="button"
            onClick={this.reload}
            className="self-start rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground"
          >
            Reload
          </button>
        </div>
      </div>
    );
  }
}
