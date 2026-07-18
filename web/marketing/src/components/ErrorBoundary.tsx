import { Component, type ErrorInfo, type ReactNode } from "react";

interface Props {
  children: ReactNode;
}

interface State {
  error: Error | null;
}

/**
 * Page-level error boundary. Catches render-time exceptions and
 * shows a clean fallback so a single broken page never takes down
 * the whole marketing site.
 */
export default class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // eslint-disable-next-line no-console
    console.error("[marketing] render error:", error, info);
  }

  render() {
    if (this.state.error) {
      return (
        <div
          role="alert"
          style={{
            padding: "var(--sp-7) var(--sp-5)",
            maxWidth: "640px",
            margin: "0 auto",
            textAlign: "center",
            color: "var(--text-secondary)",
          }}
        >
          <h1 style={{ color: "var(--text-primary)" }}>
            Something went wrong on this page
          </h1>
          <p>
            We&apos;ve logged the error. In the meantime, you can{" "}
            <a href="/">return to the home page</a> or{" "}
            <a href="mailto:support@orvix.email">tell us what happened</a>.
          </p>
        </div>
      );
    }
    return this.props.children;
  }
}
