import { Link } from "react-router-dom";
import { Check, HelpCircle } from "lucide-react";
import SEO from "../components/SEO";
import Section from "../components/Section";
import Hero from "../components/Hero";
import Container from "../components/Container";
import PlanTable from "../components/PlanTable";
import ComparisonTable from "../components/ComparisonTable";
import PricingToggle from "../components/PricingToggle";
import FAQ, { type FaqItem } from "../components/FAQ";
import { PLANS, formatPrice } from "../lib/plans";
import { PORTAL_SIGNUP } from "../lib/links";

const FAQ_ITEMS: FaqItem[] = [
  {
    q: "How does the Free plan work?",
    a: "The Free plan is $0 forever. You get 1 custom domain, up to 5 mailboxes, 1 GB of storage, and 500 sends per day. No credit card required to start.",
  },
  {
    q: "Can I switch plans later?",
    a: "Yes. Plan changes are requested from the billing page. Entitlement limits update according to the billing service, while the new charge applies at the next renewal; there is no prorated mid-cycle charge.",
  },
  {
    q: "What is the difference between monthly and yearly billing?",
    a: "The annual price is 10× the monthly price (a 16% discount), per the plan catalog in the launch spec. Same product, same SLA terms — you just pay for 12 months up front.",
  },
  {
    q: "Do you charge per seat?",
    a: "No. Plans are priced per organization, not per mailbox. The limits on the plan (number of mailboxes, storage, sends per day) are the only thing that scales.",
  },
  {
    q: "What payment methods do you accept?",
    a: "The checkout page shows the payment methods supported by the configured billing provider. Contact sales before relying on invoice terms.",
  },
  {
    q: "Are custom discounts published?",
    a: "No custom discount program is promised on this page. The catalog prices above are the public prices.",
  },
  {
    q: "Can I get a refund?",
    a: "Refund requests must be sent to billing@orvix.email within 14 days. Eligibility is reviewed against the applicable terms and order.",
  },
];

export default function Pricing() {
  return (
    <>
      <SEO path="/pricing" />

      <Hero
        eyebrow="Pricing"
        heading={<>Simple pricing. No surprises.</>}
        subheading="Four plans, four honest price points. Switch any time. Annual billing is a 16% discount and is real — not a marketing trick."
        primaryCta={{ to: PORTAL_SIGNUP, label: "Start free", external: true }}
        secondaryCta={{ to: "/contact", label: "Contact sales" }}
      />

      <Section tight>
        <PlanTable />
      </Section>

      <Section
        eyebrow="Annual vs monthly"
        heading="One toggle, two real numbers"
        lede="Both numbers are pulled directly from the plan catalog in the launch spec. Annual is 10× monthly (a 16% discount). There is no fake 'save 80%' headline."
      >
        <Container width="narrow" style={{ textAlign: "center" }}>
          <PricingToggle />
        </Container>
      </Section>

      <Section
        alt
        eyebrow="Compare"
        heading="Every feature, every plan"
        lede="A single table so you can scan what changes between plans without jumping between pages."
      >
        <ComparisonTable />
      </Section>

      <Section eyebrow="Limits at a glance" heading="The numbers, in one place">
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))",
            gap: "var(--sp-3)",
          }}
        >
          {PLANS.map((p) => (
            <div
              key={p.id}
              className="card-static"
              style={{ textAlign: "center" }}
            >
              <h3
                style={{
                  fontSize: "var(--fs-lg)",
                  margin: 0,
                  color: "var(--text-primary)",
                }}
              >
                {p.name}
              </h3>
              <p
                style={{
                  marginTop: "var(--sp-2)",
                  fontSize: "var(--fs-2xl)",
                  fontWeight: 700,
                  color: "var(--accent)",
                  margin: 0,
                }}
              >
                {formatPrice(p.usdMonthly, "monthly")}
              </p>
              <ul
                style={{
                  listStyle: "none",
                  padding: 0,
                  margin: "var(--sp-3) 0 0",
                  display: "grid",
                  gap: "var(--sp-1)",
                  fontSize: "var(--fs-sm)",
                  color: "var(--text-secondary)",
                }}
              >
                <li>{p.domains} domain{p.domains === 1 ? "" : "s"}</li>
                <li>{p.mailboxes} mailboxes</li>
                <li>{p.sendsPerDay.toLocaleString()} sends/day</li>
              </ul>
            </div>
          ))}
        </div>
      </Section>

      <Section alt bordered id="faq">
        <Container width="narrow">
          <FAQ items={FAQ_ITEMS} />
        </Container>
      </Section>

      <Section>
        <div
          style={{
            background: "var(--bg-canvas)",
            border: "1px solid var(--border-default)",
            borderRadius: "var(--r-xl)",
            padding: "var(--sp-7) var(--sp-5)",
            textAlign: "center",
          }}
        >
          <HelpCircle
            size={28}
            style={{ color: "var(--accent)", margin: "0 auto var(--sp-3)" }}
            aria-hidden="true"
          />
          <h2
            style={{
              fontSize: "var(--fs-2xl)",
              margin: 0,
              color: "var(--text-primary)",
            }}
          >
            Not sure which plan?
          </h2>
          <p
            style={{
              marginTop: "var(--sp-2)",
              color: "var(--text-secondary)",
            }}
          >
            Tell us a bit about your team and we&apos;ll point you at the right
            plan.
          </p>
          <p style={{ marginTop: "var(--sp-4)" }}>
            <Link to="/contact" className="btn btn-primary">
              Talk to sales
            </Link>
          </p>
          <p
            style={{
              marginTop: "var(--sp-3)",
              color: "var(--text-faint)",
              fontSize: "var(--fs-xs)",
            }}
          >
            Every plan includes DKIM, SPF, DMARC, IMAP, JMAP, and a status
            page. <Check size={12} style={{ display: "inline", verticalAlign: "middle" }} />
          </p>
        </div>
      </Section>
    </>
  );
}
