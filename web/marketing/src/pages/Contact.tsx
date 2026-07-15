import SEO from "../components/SEO";
import Hero from "../components/Hero";
import Section from "../components/Section";
import Container from "../components/Container";
import ContactForm, { ContactSidebar } from "../components/ContactForm";

export default function Contact() {
  return (
    <>
      <SEO path="/contact" />

      <Hero
        eyebrow="Contact"
        heading="Talk to the right team"
        subheading="Use the form to compose your message — it'll open in your mail client pre-filled with the right address. Or email us directly. We respond within one business day."
      />

      <Section>
        <Container>
          <div
            className="contact-grid"
            style={{
              display: "grid",
              gridTemplateColumns: "1.4fr 1fr",
              gap: "var(--sp-6)",
              alignItems: "flex-start",
            }}
          >
            <ContactForm />
            <ContactSidebar />
          </div>
          <style>{`@media (max-width: 880px) { .contact-grid { grid-template-columns: 1fr !important; } }`}</style>
        </Container>
      </Section>

      <Section alt bordered>
        <Container width="narrow">
          <h2
            style={{
              fontSize: "var(--fs-2xl)",
              marginBottom: "var(--sp-3)",
              color: "var(--text-primary)",
            }}
          >
            Already a customer?
          </h2>
          <p
            style={{
              color: "var(--text-secondary)",
              fontSize: "var(--fs-sm)",
              lineHeight: 1.7,
            }}
          >
            For account, billing, or product questions, please{" "}
            <a href="/login" style={{ color: "var(--accent)" }}>
              sign in
            </a>{" "}
            and use in-product support. We can see your account, your plan, and
            your support history, which means we can usually answer faster.
          </p>
          <p
            style={{
              marginTop: "var(--sp-3)",
              color: "var(--text-secondary)",
              fontSize: "var(--fs-sm)",
              lineHeight: 1.7,
            }}
          >
            For security disclosures, see the{" "}
            <a href="/security" style={{ color: "var(--accent)" }}>
              security page
            </a>{" "}
            for our disclosure policy and PGP key.
          </p>
        </Container>
      </Section>
    </>
  );
}
