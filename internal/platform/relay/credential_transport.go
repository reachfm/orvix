package relay

// Credential transport policy (Phase 3A, Fix D).
//
// THE DEFECT THIS CLOSES
//
// connectAndAuth previously sent `AUTH PLAIN <base64(user\0pass)>` whenever a
// credential was configured, with no reference to whether the session was
// encrypted. Two configurations therefore leaked the relay credential:
//
//  1. ConnSecurity == "none" — AUTH PLAIN travels in cleartext over TCP. The
//     credential is base64, not encrypted; anyone on path recovers it.
//  2. TLSValidation == "opportunistic" — tlsConfigFor sets
//     InsecureSkipVerify: true, so the certificate chain and the hostname are
//     BOTH unverified. Any active on-path attacker terminates the TLS session
//     with a self-signed certificate and the client happily authenticates to
//     them.
//
// THE POLICY
//
// A stored credential may only ever be transmitted over a TLS session whose
// certificate chain AND hostname have been verified. This is enforced at two
// layers:
//
//   - ValidateCredentialTransport, evaluated during route resolution BEFORE
//     the credential is decrypted, so a non-compliant provider's secret is
//     never even materialized in process memory.
//   - requireVerifiedTLSForAuth, evaluated inside connectAndAuth immediately
//     before the AUTH command, keyed off the ACTUAL negotiated session rather
//     than the configuration. This is the backstop: it holds even for a caller
//     that bypassed route resolution (TestConnection, a future code path, or a
//     test), and it catches the case where STARTTLS was configured but the
//     server silently declined to upgrade.
//
// A provider with NO credential is unaffected — unauthenticated relay over a
// plaintext or opportunistic channel remains a legitimate configuration
// (internal smarthosts), because there is no secret to expose.

// requiresVerifiedTLS reports whether this connection security mode produces a
// session whose peer identity has been cryptographically verified.
func (c ConnSecurity) isEncrypted() bool {
	return c == ConnSecurityStartTLS || c == ConnSecurityImplicitTLS
}

// ValidateCredentialTransport refuses any provider that would transmit a
// stored credential over a channel that is not verified TLS.
//
// It is deliberately evaluated BEFORE decryptCredential in the route
// resolution path: a misconfigured provider's secret is never decrypted, so it
// cannot be leaked by a subsequent bug, panic, or log line.
func ValidateCredentialTransport(p Provider) error {
	if !p.HasSecret() {
		// Nothing to protect: unauthenticated relay may use any transport.
		return nil
	}
	if !p.ConnSecurity.isEncrypted() {
		// Cleartext AUTH PLAIN. Never permitted.
		return ErrInsecureCredentialTransport
	}
	if p.TLSValidation != TLSValidationStrict {
		// Opportunistic validation means InsecureSkipVerify — an on-path
		// attacker can present any certificate and harvest the credential.
		return ErrInsecureCredentialTransport
	}
	return nil
}

// requireVerifiedTLSForAuth is the connection-time backstop. `negotiatedTLS`
// reflects what actually happened on the wire (an implicit-TLS handshake or a
// completed STARTTLS upgrade), not what was configured.
//
// The `p.TLSValidation` term matters because a strict-looking configuration is
// only meaningful if tlsConfigFor did NOT set InsecureSkipVerify; keeping both
// terms here means the two functions cannot drift apart silently.
func requireVerifiedTLSForAuth(p Provider, negotiatedTLS bool) error {
	if !negotiatedTLS {
		return ErrInsecureCredentialTransport
	}
	if p.TLSValidation != TLSValidationStrict {
		return ErrInsecureCredentialTransport
	}
	return nil
}
