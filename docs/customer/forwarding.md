# Setting Up Email Forwarding

Email forwarding automatically sends copies of incoming messages to another email address. The original messages remain in your mailbox unless you choose to delete them after forwarding.

## Enabling Forwarding

1. Sign in to [Orvix Webmail](webmail.md).
2. Click the gear icon (**Settings**) in the top-right corner.
3. Go to **Forwarding**.
4. Toggle **Enable Forwarding** to **On**.
5. Enter the destination email address.
6. Choose a forwarding behavior:

| Option | Description |
| ------ | ----------- |
| **Keep a copy** | Forwarded messages remain in your inbox. |
| **Delete after forwarding** | The original message is deleted after forwarding. |
| **Mark as read** | Forwarded messages are kept and marked as read. |

7. Click **Save**.

## Forwarding to Multiple Addresses

To forward to more than one address:

1. Use a [group](groups.md) as the forward destination.
2. Create the group with all target recipients.
3. Set your forwarding destination to the group address.

Or, add multiple forwarding rules from the **Forwarding** page. Each rule can forward to a different address.

## Domain-Level Forwarding

Admins can set up forwarding at the domain level:

1. Go to **Domains** > your domain > **Mailboxes**.
2. Find the mailbox and click **Edit**.
3. Under **Forwarding**, enter the destination address.
4. Click **Save**.

## Conditional Forwarding

Use [mail rules](rules.md) to forward only certain messages:

1. Create a new rule.
2. Set conditions (e.g., "From contains `manager`").
3. Set action to "Forward to" and enter the destination.
4. Click **Save**.

## Important Notes

- Forwarded messages retain their original headers (From, Subject, Date).
- Forwarding does not affect your mailbox quota unless you choose to keep a copy.
- Do not create a forwarding loop (A → B → A). Orvix detects and breaks forwarding loops.
- Some receiving servers may treat forwarded mail as spoofed if SPF/DKIM checks fail. This is normal behavior for forwarded mail.
