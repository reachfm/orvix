import { useEffect, useState } from "react";
import { Link, NavLink, useLocation } from "react-router-dom";
import { Menu, X } from "lucide-react";
import Container from "./Container";
import Logo from "./Logo";

const NAV_LINKS = [
  { to: "/features", label: "Features" },
  { to: "/enterprise", label: "Enterprise" },
  { to: "/security", label: "Security" },
  { to: "/pricing", label: "Pricing" },
  { to: "/docs", label: "Docs" },
  { to: "/status", label: "Status" },
];

export default function TopNav() {
  const [open, setOpen] = useState(false);
  const location = useLocation();

  // Close the mobile menu on route change.
  useEffect(() => {
    setOpen(false);
  }, [location.pathname]);

  return (
    <header
      role="banner"
      style={{
        position: "sticky",
        top: 0,
        zIndex: 50,
        height: "var(--nav-height)",
        background: "color-mix(in oklab, var(--bg-app) 92%, transparent)",
        backdropFilter: "saturate(140%) blur(10px)",
        WebkitBackdropFilter: "saturate(140%) blur(10px)",
        borderBottom: "1px solid var(--border-subtle)",
      }}
    >
      <Container
        as="nav"
        aria-label="Primary"
        className="nav-row"
        width="wide"
      >
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            height: "var(--nav-height)",
          }}
        >
          <Link
            to="/"
            aria-label="Orvix home"
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: "var(--sp-2)",
              textDecoration: "none",
              color: "var(--text-primary)",
              fontWeight: 700,
              fontSize: "var(--fs-lg)",
            }}
          >
            <Logo size={28} />
            <span>Orvix</span>
          </Link>

          <ul
            className="nav-links-desktop"
            style={{
              display: "flex",
              alignItems: "center",
              gap: "var(--sp-2)",
              listStyle: "none",
              margin: 0,
              padding: 0,
            }}
          >
            {NAV_LINKS.map((link) => (
              <li key={link.to}>
                <NavLink
                  to={link.to}
                  end={link.to === "/"}
                  style={({ isActive }) => ({
                    display: "inline-block",
                    padding: "var(--sp-2) var(--sp-3)",
                    borderRadius: "var(--r-sm)",
                    color: isActive ? "var(--text-primary)" : "var(--text-secondary)",
                    background: isActive ? "var(--bg-elevated)" : "transparent",
                    fontSize: "var(--fs-sm)",
                    fontWeight: 500,
                    textDecoration: "none",
                    transition:
                      "color var(--dur-1) var(--ease-out), background var(--dur-1) var(--ease-out)",
                  })}
                >
                  {link.label}
                </NavLink>
              </li>
            ))}
          </ul>

          <div
            className="nav-cta-desktop"
            style={{
              display: "flex",
              alignItems: "center",
              gap: "var(--sp-2)",
            }}
          >
            <Link
              to="/login"
              className="btn btn-ghost btn-sm"
              aria-label="Sign in to the Orvix portal"
            >
              Sign in
            </Link>
            <Link
              to="/signup"
              className="btn btn-primary btn-sm"
              aria-label="Start a free Orvix account"
            >
              Start free
            </Link>
          </div>

          <button
            className="nav-toggle"
            aria-label={open ? "Close menu" : "Open menu"}
            aria-expanded={open}
            aria-controls="mobile-menu"
            onClick={() => setOpen((v) => !v)}
            style={{
              display: "none",
              background: "transparent",
              border: "1px solid var(--border-default)",
              borderRadius: "var(--r-sm)",
              padding: "var(--sp-2)",
              color: "var(--text-primary)",
              cursor: "pointer",
            }}
          >
            {open ? <X size={20} /> : <Menu size={20} />}
          </button>
        </div>

        {open && (
          <div
            id="mobile-menu"
            className="nav-mobile"
            style={{
              padding: "var(--sp-4) 0 var(--sp-5)",
              borderTop: "1px solid var(--border-subtle)",
            }}
          >
            <ul
              style={{
                display: "flex",
                flexDirection: "column",
                gap: "var(--sp-1)",
                listStyle: "none",
                margin: 0,
                padding: 0,
              }}
            >
              {NAV_LINKS.map((link) => (
                <li key={link.to}>
                  <NavLink
                    to={link.to}
                    end={link.to === "/"}
                    style={({ isActive }) => ({
                      display: "block",
                      padding: "var(--sp-3) var(--sp-4)",
                      borderRadius: "var(--r-sm)",
                      color: isActive ? "var(--text-primary)" : "var(--text-secondary)",
                      background: isActive ? "var(--bg-elevated)" : "transparent",
                      fontSize: "var(--fs-base)",
                      fontWeight: 500,
                    })}
                  >
                    {link.label}
                  </NavLink>
                </li>
              ))}
            </ul>
            <div
              style={{
                display: "grid",
                gridTemplateColumns: "1fr 1fr",
                gap: "var(--sp-2)",
                marginTop: "var(--sp-4)",
              }}
            >
              <Link to="/login" className="btn btn-secondary">
                Sign in
              </Link>
              <Link to="/signup" className="btn btn-primary">
                Start free
              </Link>
            </div>
          </div>
        )}
      </Container>

      <style>{`
        @media (max-width: 880px) {
          .nav-links-desktop,
          .nav-cta-desktop {
            display: none !important;
          }
          .nav-toggle {
            display: inline-flex !important;
          }
        }
      `}</style>
    </header>
  );
}
