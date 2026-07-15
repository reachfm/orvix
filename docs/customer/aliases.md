# Creating Email Aliases

An alias is an alternative email address that delivers to an existing mailbox. It has no separate login or storage.

## Creating an Alias

1. Go to **Domains** and select your domain.
2. Click the **Aliases** tab.
3. Click **Create Alias**.
4. Configure the alias:

| Field | Description |
| ----- | ----------- |
| **Alias** | The local part of the alias (e.g., `support` for `support@example.com`). |
| **Destination** | The mailbox or group that receives mail sent to the alias. |

5. Click **Create**.

## Example Use Cases

- `support@example.com` → `john@example.com` (department addresses routing to an individual)
- `firstname.lastname@example.com` → `flastname@example.com` (alternate name format)
- `admin@example.com` → `john@example.com` and `jane@example.com` (multiple recipients — use a [group](groups.md) instead)
- `hello@example.com` → `john@example.com` (catch-all for common misspellings)

## Catch-All Alias

A catch-all alias captures any email sent to a non-existent address on your domain:

1. Go to **Domains** > your domain > **Aliases**.
2. Click **Create Alias**.
3. Set the alias to `*` (asterisk).
4. Select the destination mailbox.
5. Click **Create**.

**Caution**: A catch-all alias will accept all mail, including spam sent to random addresses on your domain. Use it sparingly.

## Managing Aliases

- **Edit** an alias to change the destination.
- **Delete** an alias to stop receiving mail at that address.

Deleting an alias does not affect the destination mailbox or the mail already delivered.

## Limits

- An alias can forward to only one destination. Use a [group](groups.md) to deliver to multiple mailboxes.
- There is no limit on the number of aliases per domain, subject to your plan's limits.
