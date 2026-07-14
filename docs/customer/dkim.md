# Setting Up DKIM Records

DKIM (DomainKeys Identified Mail) adds a digital signature to outgoing messages, allowing receiving servers to verify that the email was sent by an authorized server and has not been tampered with.

## How DKIM Works

1. Orvix generates a public/private key pair for your domain.
2. The private key is used by Orvix to sign outgoing emails.
3. The public key is published in your domain's DNS as a DKIM record.
4. Receiving servers use the public key to verify the signature.

## Locating Your DKIM Values

1. Go to **Domains** and select your verified domain.
2. Click the **DNS** tab.
3. Find the **DKIM Record** section. Orvix displays the required DKIM record.

## Adding the DKIM Record

The DKIM record is added as a TXT record. It will look similar to:

| Type | Host | Value |
| ---- | ---- | ----- |
| TXT | `orvix._domainkey` | `v=DKIM1; k=rsa; p=<public_key>` |

### Steps

1. Log in to your DNS provider's control panel.
2. Navigate to DNS management for your domain.
3. Add a new TXT record.
4. Set the host to the exact selector shown in Orvix (e.g., `orvix._domainkey`).
5. Paste the full DKIM value exactly as displayed.
6. Save the record.

## Rotating DKIM Keys

You can rotate your DKIM keys from the Orvix dashboard:

1. Go to **Domains** > your domain > **DNS**.
2. Next to the DKIM record, click **Rotate Key**.
3. A new key pair is generated. You will be shown both the old and new public keys.
4. Add the new DKIM record to your DNS **without removing the old one yet**.
5. Wait 48 hours for all cached DNS entries to expire.
6. Remove the old DKIM record from your DNS.
7. Click **Confirm Rotation** in Orvix.

## Verifying

1. In Orvix, go to **Domains** > your domain > **DNS**.
2. Click **Check Records**.
3. A green checkmark confirms your DKIM record is valid.
