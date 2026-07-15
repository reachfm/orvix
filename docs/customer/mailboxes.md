# Creating and Managing Mailboxes

Mailboxes are where email messages are stored for individual users on your domain.

## Creating a Mailbox

1. Go to **Domains** and select your domain.
2. Click the **Mailboxes** tab.
3. Click **Create Mailbox**.
4. Fill in the details:

| Field | Description |
| ----- | ----------- |
| **Email** | The local part of the address (e.g., `john` for `john@example.com`). |
| **Display Name** | The name shown in the From field of outgoing email. |
| **Password** | The mailbox password. The user uses this to sign in to webmail and configure IMAP/SMTP clients. |

5. Click **Create**.

After creation, Orvix displays the mailbox connection details (IMAP, SMTP, server addresses) that the user needs for email clients.

## Managing Mailboxes

From the **Mailboxes** tab, you can:

- **Edit** a mailbox to change the display name or password.
- **Disable** a mailbox to prevent sign-in while preserving all email.
- **Enable** a previously disabled mailbox.
- **Delete** a mailbox permanently. **All email is deleted and cannot be recovered.**

## Setting Mailbox Password

Users can change their own password from webmail:

1. Sign in to webmail at [https://webmail.orvix.com](https://webmail.orvix.com).
2. Go to **Settings** > **Password**.
3. Enter the current password and the new password.
4. Click **Save**.

Admins and Operators can reset a user's password:

1. Go to **Domains** > your domain > **Mailboxes**.
2. Find the mailbox and click **Edit**.
3. Enter a new password in the password field.
4. Click **Save**.

## Connection Settings

Provide these settings to users for configuring email clients:

| Protocol | Server | Port | Encryption |
| -------- | ------ | ---- | ---------- |
| IMAP | `imap.orvix.com` | 993 | SSL/TLS |
| POP3 | `pop3.orvix.com` | 995 | SSL/TLS |
| SMTP | `smtp.orvix.com` | 587 | STARTTLS |

The username is always the full email address (e.g., `john@example.com`).
