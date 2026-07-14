# Release Signing Key Rotation

## Overview

Orvix release artifacts (tarballs, manifests, SBOMs) are signed with an Ed25519 private key. The signature is verified by the installer before applying any release. This document covers the full lifecycle of the signing key.

## Key Identifiers

| Property | Value |
|---|---|
| Algorithm | Ed25519 (curve25519) |
| Key ID | SHA-256 of the public key (last 8 hex chars) |
| Current public key | `release/trust/orvix-release-signing.pub.pem` |
| Current key ID | `d8a7c3f1` (example; derive via `openssl pkey -in key.pem -pubout | sha256sum`) |
| CI secret | `ORVIX_RELEASE_SIGNING_KEY_PEM_B64` |

## Key provisioning

### Generating a new signing key

```bash
# Generate Ed25519 private key
openssl genpkey -algorithm ed25519 -out orvix-release-signing.pem
chmod 0600 orvix-release-signing.pem

# Extract public key
openssl pkey -in orvix-release-signing.pem -pubout -out orvix-release-signing.pub.pem

# Base64-encode the private key for CI
base64 -w0 < orvix-release-signing.pem
```

### Setting the CI secret

1. Go to GitHub repository Settings → Secrets and variables → Actions
2. Add or update `ORVIX_RELEASE_SIGNING_KEY_PEM_B64`
3. Paste the base64-encoded private key (single line, no newlines)
4. The CI `release-bundle.yml` reads this secret and signs artifacts

### Trust root

The public key is committed at `release/trust/orvix-release-signing.pub.pem`. This file is the trust root for release verification:

- The installer (`install.sh`) verifies artifact signatures against this public key
- The bundle `build-release-bundle.sh` embeds this public key in `release/trust/`
- Any release whose signature does not verify against this key is rejected

No private key material is ever committed to the repository.

## Key rotation procedure

### When to rotate

- Scheduled rotation (recommended every 12 months)
- Known or suspected private key compromise
- Post-incident recovery after a security event
- Change of trusted signing personnel

### Rotation steps

1. **Generate a new key pair**

   ```bash
   openssl genpkey -algorithm ed25519 -out orvix-release-signing-v2.pem
   openssl pkey -in orvix-release-signing-v2.pem -pubout -out orvix-release-signing-v2.pub.pem
   KEY_ID=$(openssl pkey -in orvix-release-signing-v2.pem -pubout 2>/dev/null | sha256sum | cut -c1-8)
   ```

2. **Compute the key ID**

   ```bash
   openssl pkey -in orvix-release-signing-v2.pem -pubout | sha256sum | cut -c1-8
   ```

3. **Update the CI secret**

   ```bash
   base64 -w0 < orvix-release-signing-v2.pem | pbcopy
   ```

   Paste into GitHub → Settings → Secrets → `ORVIX_RELEASE_SIGNING_KEY_PEM_B64`

4. **Commit the new public key**

   ```bash
   cp orvix-release-signing-v2.pub.pem release/trust/orvix-release-signing.pub.pem
   git add release/trust/orvix-release-signing.pub.pem
   git commit -m "chore: rotate release signing key (key ID: $KEY_ID)"
   git push
   ```

5. **Sign the next release with the new key** — the CI workflow automatically uses `ORVIX_RELEASE_SIGNING_KEY_PEM_B64` from the secret

6. **Archive the old private key** in an encrypted offline backup (e.g., GPG-encrypted USB, vault)

## Overlap period

During the overlap between key versions:

- The old key must remain in CI for re-signing any hotfix releases on the previous branch
- Set `ORVIX_RELEASE_SIGNING_KEY_PEM_B64` to the new key
- The old key can be stored in a separate secret (e.g., `ORVIX_RELEASE_SIGNING_KEY_PEM_B64_OLD`)
- CI can reference the old key for maintenance branches:
  ```yaml
  env:
    ORVIX_RELEASE_SIGNING_KEY_PEM_B64: ${{ secrets.ORVIX_RELEASE_SIGNING_KEY_PEM_B64_OLD }}
  ```

The overlap period should not exceed 30 days. After expiry, remove the old key from CI secrets.

## Key revocation

If a key is compromised:

1. **Immediately** generate a new key pair (see rotation steps)
2. **Immediately** update `ORVIX_RELEASE_SIGNING_KEY_PEM_B64` in GitHub secrets
3. **Immediately** commit the new public key
4. **Revoke the old key** by publishing a revocation notice in the repository:
   - Create `docs/advisories/YYYY-MM-DD-key-revocation.md`
   - Include the compromised key ID and SHA-256 fingerprint
   - State the date and time of revocation
   - Provide instructions for verifying releases against the new key
5. **Re-sign all active releases** with the new key:
   ```bash
   for asset in dist/*.tar.gz dist/*.manifest.json dist/*.sbom.spdx; do
     bash release/scripts/sign-release-artifact.sh "$asset" new-key.pem "$asset.sig"
   done
   ```
6. **Upload re-signed assets** to GitHub Releases (overwrite existing)

## Rollback to a previous key

If a key rotation causes verification failures:

1. Restore the previous private key from encrypted backup
2. Set `ORVIX_RELEASE_SIGNING_KEY_PEM_B64` to the previous key
3. Revert the public key commit:
   ```bash
   git revert HEAD
   git push
   ```
4. Re-sign and re-upload current release assets
5. Investigate the rotation failure before re-attempting

## Emergency replacement

If the private key is lost and no backup exists:

1. Generate a new key pair (see rotation steps)
2. Update CI secret with the new key
3. Commit the new public key
4. Re-sign all active release assets
5. Publish an advisory explaining that the previous key was lost and releases are now signed with the replacement key
6. All current signatures from the lost key are invalidated — operators must re-download re-signed assets

## Verifying key identity

```bash
# Extract the key ID from a public key file
openssl pkey -in release/trust/orvix-release-signing.pub.pem -pubin 2>/dev/null \
  | sha256sum | cut -c1-8

# Verify a signature
bash release/scripts/verify-release-signature.sh \
  orvix-enterprise-mail-stable-linux-amd64.tar.gz \
  orvix-enterprise-mail-stable-linux-amd64.tar.gz.sig \
  release/trust/orvix-release-signing.pub.pem
```

## Audit log

Every key rotation MUST be recorded in the git history with:
- The old key ID
- The new key ID
- The reason for rotation (scheduled / compromise / recovery)
- The operator who performed the rotation
- The date and time of rotation
