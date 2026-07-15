import React, { Suspense } from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter, Route, Routes, useLocation } from "react-router-dom";

import "./styles/tokens.css";
import "./styles/base.css";
import "./styles/components.css";
import "./styles/pages.css";

import TopNav from "./components/TopNav";
import Footer from "./components/Footer";
import CookieBanner from "./components/CookieBanner";
import ErrorBoundary from "./components/ErrorBoundary";
import { PAGE_LOADERS } from "./lib/route-table";

const App = () => {
  return (
    <BrowserRouter>
        <ErrorBoundary>
          <a href="#main" className="skip-link">
            Skip to main content
          </a>
          <TopNav />
          <main id="main" style={{ flex: 1 }}>
            <ScrollToTop />
            <Suspense fallback={<RouteFallback />}>
              <Routes>
                {Object.entries(PAGE_LOADERS).map(([path, Component]) => (
                  <Route
                    key={path}
                    path={path}
                    element={<Component />}
                  />
                ))}
              </Routes>
            </Suspense>
          </main>
          <Footer />
          <CookieBanner />
        </ErrorBoundary>
    </BrowserRouter>
  );
};

function ScrollToTop() {
  const location = useLocation();
  React.useEffect(() => {
    window.scrollTo(0, 0);
  }, [location.pathname, location.search]);
  return null;
}

function RouteFallback() {
  return (
    <div
      role="status"
      aria-live="polite"
      style={{
        minHeight: "60vh",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        color: "var(--text-muted)",
      }}
    >
      Loading…
    </div>
  );
}

const root = document.getElementById("root");
if (!root) {
  throw new Error("#root not found");
}

ReactDOM.createRoot(root).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
