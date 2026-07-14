# Service Status Page

The Orvix status page provides real-time information about system availability, ongoing incidents, and scheduled maintenance.

## Accessing the Status Page

Visit [https://status.orvix.com](https://status.orvix.com) — no sign-in required.

Bookmark the page to check it whenever you experience unexpected behavior.

## What the Status Page Shows

### Component Status

Each Orvix service component shows its current status:

| Status | Meaning |
| ------ | ------- |
| **Operational** | The component is working normally. |
| **Degraded Performance** | The component is running but slower than usual. |
| **Partial Outage** | Some users or regions are affected. |
| **Major Outage** | The service is unavailable for all users. |
| **Under Maintenance** | The component is undergoing planned maintenance. |

### Tracked Components

- **Webmail** — Webmail application availability.
- **SMTP Outbound** — Outgoing email delivery.
- **SMTP Inbound (MX)** — Incoming email receiving.
- **IMAP/POP3** — Email client connectivity.
- **API** — Public API availability.
- **Dashboard** — Management interface availability.
- **Billing** — Payment processing and invoicing.

## Incident History

The status page includes a timeline of:

- Active incidents, with regular status updates.
- Resolved incidents, with a post-mortem summary.
- Upcoming and completed maintenance windows.

## Subscribing to Updates

Stay informed without checking the page:

1. Click **Subscribe to Updates** on the status page.
2. Choose your notification method: email, SMS, RSS feed, or webhook.
3. Select which components and severity levels you want to track.
4. Click **Subscribe**.

## Incident Response

When an incident occurs:

1. The status page is updated within 5 minutes of detection.
2. Regular updates are posted as the incident is investigated and resolved.
3. After resolution, a summary is published explaining the cause and steps taken to prevent recurrence.

## Uptime History

The status page displays the historical uptime percentage for each component, typically with a 90-day rolling view.

## API Status Endpoint

You can also check status programmatically:

```
GET https://status.orvix.com/api/v2/status.json
```

This returns the current status of all components in JSON format, suitable for integration with your own monitoring dashboards.
