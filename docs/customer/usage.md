# Understanding Usage Metrics and Limits

Orvix tracks resource usage across your organization to help you monitor consumption against your plan limits.

## Viewing Usage

1. Go to **Organization** > **Usage**.
2. The dashboard displays current consumption and plan limits for each metric.

## Tracked Metrics

| Metric | Description |
| ------ | ----------- |
| **Mailboxes** | Number of active mailboxes. Disabled mailboxes do not count. |
| **Total Storage** | Combined storage used by all mailboxes. |
| **Domains** | Number of verified domains in your organization. |
| **Aliases** | Total aliases across all domains. |
| **Groups** | Total groups across all domains. |
| **Outbound Messages** | Emails sent in the current billing period. Count resets each month. |
| **Inbound Messages** | Emails received in the current billing period. |
| **API Requests** | Number of API calls in the current billing period. |
| **Data Transfer** | Total bandwidth used for IMAP, SMTP, and API connections. |

## Usage Period

Usage metrics reset at the start of each billing period (typically the 1st of the month for monthly plans, or the anniversary date for annual plans).

## Warning Thresholds

You receive email notifications when:

- Any metric reaches **80%** of its limit.
- Any metric reaches **95%** of its limit.
- Any metric reaches **100%** of its limit.

Notifications are sent to the organization owner and all admins.

## What Happens When You Hit a Limit

| Limit | Effects |
| ----- | ------- |
| Mailbox count | Cannot create additional mailboxes. |
| Storage | Existing mailboxes at their quota stop receiving mail. |
| Domains | Cannot add new domains. |
| Outbound messages | Outgoing mail is deferred until the next billing period. |

## Increasing Limits

- [Upgrade your plan](plans.md) for higher limits.
- Contact Orvix support to discuss custom limits for Enterprise plans.
- Usage limits cannot be temporarily increased for lower-tier plans.

## Exporting Usage Data

1. Go to **Organization** > **Usage**.
2. Click **Export**.
3. Choose the time range and format (CSV or JSON).
4. Click **Download**.
