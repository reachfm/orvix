# Understanding Mailbox Quotas and Limits

Each mailbox in Orvix has a storage quota that limits how much email it can store.

## Quota Types

| Quota | Description |
| ----- | ----------- |
| **Storage Quota** | Maximum total disk space the mailbox can use across all folders. |
| **Message Count** | Maximum number of messages the mailbox can hold. |

## Default Quotas

Default quotas are determined by your [plan](plans.md). Plan limits are per-mailbox, not shared across mailboxes.

| Plan | Storage per Mailbox | 
| ---- | ------------------- |
| Starter | 5 GB |
| Professional | 25 GB |
| Business | 50 GB |
| Enterprise | Unlimited |

## Viewing Mailbox Quota Usage

1. Go to **Domains** > your domain > **Mailboxes**.
2. Each mailbox shows a usage bar with current usage and quota limit.
3. Click on a mailbox to see a folder-by-folder breakdown.

Users can also see their own quota usage within webmail.

## What Happens When Quota Is Exceeded

- **95%**: The user receives a warning email.
- **100%**: The mailbox stops accepting new incoming messages. Senders receive a bounce notification stating the recipient's mailbox is full.
- Outgoing mail is not affected by storage quota.

## Increasing a Mailbox Quota

If your plan allows custom quotas:

1. Go to **Domains** > your domain > **Mailboxes**.
2. Click the mailbox you want to modify.
3. Under **Quota**, enter the new maximum storage size (in GB).
4. Click **Save**.

The new quota takes effect immediately.

## Plan-Level Quotas

Your plan may also include limits on:

- **Total mailboxes** across your organization.
- **Total storage** across all mailboxes.
- **Outbound messages per day** per mailbox and per domain.

Check **Organization** > **Usage** for your current plan limits and consumption.

## Reducing Quota Usage

- Delete old or large emails from the Trash and Spam folders.
- Empty the Trash folder regularly.
- Download large attachments and delete the messages.
- Set up automatic archiving using [mail rules](rules.md).
- Upgrade your plan if you consistently hit quota limits.
