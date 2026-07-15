import { useEffect, useState } from "react";
import SEO from "../components/SEO";
import Hero from "../components/Hero";
import Section from "../components/Section";
import Container from "../components/Container";
import StatusTile, { type StatusState } from "../components/StatusTile";

type HealthState = "checking" | "healthy" | "unavailable";

export default function Status() {
  const [health, setHealth] = useState<HealthState>("checking");

  useEffect(() => {
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 5000);
    fetch("/api/v1/health", { signal: controller.signal, credentials: "omit" })
      .then(async (response) => {
        if (!response.ok) throw new Error("health endpoint unavailable");
        const body = await response.json() as { status?: string };
        setHealth(body.status === "ok" ? "healthy" : "unavailable");
      })
      .catch(() => setHealth("unavailable"))
      .finally(() => window.clearTimeout(timeout));
    return () => {
      controller.abort();
      window.clearTimeout(timeout);
    };
  }, []);

  const state: StatusState = health === "healthy" ? "operational" : health === "checking" ? "maintenance" : "degraded";
  const note = health === "healthy"
    ? "The public API health endpoint returned status ok."
    : health === "checking"
      ? "Checking the public API health endpoint."
      : "The public health endpoint could not confirm service health. Contact support for current information.";

  return (
    <>
      <SEO path="/status" />
      <Hero
        eyebrow="Status"
        heading="Current public service check"
        subheading="This page reports only what the public health endpoint can verify. Orvix does not yet publish component uptime history or incident subscriptions on this page."
        primaryCta={{ to: "/contact", label: "Contact support" }}
        secondaryCta={{ to: "/security", label: "Report a security issue" }}
      />
      <Section tight>
        <Container width="narrow">
          <StatusTile service="Orvix API" state={state} note={note} />
          <div className="card-static" style={{ marginTop: "var(--sp-4)" }}>
            <h2>What this check means</h2>
            <p style={{ color: "var(--text-secondary)" }}>
              A healthy result confirms that the Orvix API process answered its liveness endpoint. It is not an uptime guarantee and does not independently prove SMTP, IMAP, billing, or third-party provider health.
            </p>
          </div>
        </Container>
      </Section>
    </>
  );
}
