import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import Home from "../../src/pages/Home";
import Pricing from "../../src/pages/Pricing";
import Features from "../../src/pages/Features";
import Enterprise from "../../src/pages/Enterprise";
import Security from "../../src/pages/Security";
import Docs from "../../src/pages/Docs";
import Api from "../../src/pages/Api";
import Status from "../../src/pages/Status";
import About from "../../src/pages/About";
import Contact from "../../src/pages/Contact";
import Blog from "../../src/pages/Blog";
import BlogWelcome from "../../src/pages/BlogWelcome";
import LegalIndex from "../../src/pages/LegalIndex";
import LegalTerms from "../../src/pages/LegalTerms";
import LegalPrivacy from "../../src/pages/LegalPrivacy";
import LegalAup from "../../src/pages/LegalAup";
import LegalCookies from "../../src/pages/LegalCookies";
import LegalData from "../../src/pages/LegalData";
import NotFound from "../../src/pages/NotFound";

/**
 * Smoke test: every page renders without throwing. Catches a whole
 * class of "looks fine in editor, blows up at runtime" mistakes.
 */

const PAGES: Array<[string, React.ComponentType]> = [
  ["Home", Home],
  ["Pricing", Pricing],
  ["Features", Features],
  ["Enterprise", Enterprise],
  ["Security", Security],
  ["Docs", Docs],
  ["Api", Api],
  ["Status", Status],
  ["About", About],
  ["Contact", Contact],
  ["Blog", Blog],
  ["BlogWelcome", BlogWelcome],
  ["LegalIndex", LegalIndex],
  ["LegalTerms", LegalTerms],
  ["LegalPrivacy", LegalPrivacy],
  ["LegalAup", LegalAup],
  ["LegalCookies", LegalCookies],
  ["LegalData", LegalData],
  ["NotFound", NotFound],
];

function renderInRouter(Page: React.ComponentType) {
  return render(
    <MemoryRouter>
      <Page />
    </MemoryRouter>,
  );
}

describe("page render smoke tests", () => {
  for (const [name, Page] of PAGES) {
    it(`${name} renders without throwing`, () => {
      const { container } = renderInRouter(Page);
      expect(container).toBeTruthy();
    });
  }
});
