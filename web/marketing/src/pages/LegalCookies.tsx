import SEO from "../components/SEO";
import Section from "../components/Section";
import Container from "../components/Container";
import Hero from "../components/Hero";

export default function LegalCookies() {
  return (
    <>
      <SEO path="/legal/cookies" />
      <Hero eyebrow="Legal" heading="Cookie policy" subheading="The marketing site uses only the browser storage needed for site preferences. No analytics SDK is included in this release." />
      <Section><Container width="narrow"><div className="legal-doc prose">
        <h2>Necessary storage</h2>
        <p>The site may store language, direction, theme, and cookie-notice choices so those preferences survive navigation or a later visit.</p>
        <h2>Authentication</h2>
        <p>The marketing site does not read customer or administrator session credentials. Sign-in and signup links lead to the customer portal.</p>
        <h2>Analytics and advertising</h2>
        <p>No analytics SDK, advertising pixel, or analytics cookie is included in this release.</p>
        <h2>Changing preferences</h2>
        <p>You can clear site data using your browser controls. Necessary preferences may be created again when you use the corresponding site control.</p>
        <h2>Contact</h2>
        <p>Email <a href="mailto:privacy@orvix.email">privacy@orvix.email</a> with cookie or privacy questions.</p>
      </div></Container></Section>
    </>
  );
}
