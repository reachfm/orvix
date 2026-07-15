# Setting Up Vacation Auto-Replies

A vacation auto-reply sends an automatic response to people who email you while you are away.

## Enabling Vacation Mode

1. Sign in to [Orvix Webmail](webmail.md).
2. Click the gear icon (**Settings**) in the top-right corner.
3. Go to **Vacation**.
4. Toggle **Enable Vacation Reply** to **On**.

## Configuring Your Reply

| Setting | Description |
| ------- | ----------- |
| **Subject** | The subject line of the auto-reply (e.g., "Out of Office"). |
| **Message** | The body of the auto-reply. Keep it brief and professional. |
| **Start Date** | The date to begin sending auto-replies (optional). Leave empty to start immediately. |
| **End Date** | The date to stop sending auto-replies (optional). Leave empty to disable manually. |

## Additional Options

- **Reply frequency**: Control how often auto-replies are sent to the same sender. Options: once per day (default), once per week, or once per contact.
- **Send replies to external senders only**: Only send auto-replies to people outside your domain.
- **Internal message**: Set a different reply message for people within your organization.

## Sample Vacation Message

```
Subject: Out of Office

Thank you for your email. I am currently out of the office and will return on [date].

If you need immediate assistance, please contact [colleague name] at [colleague email].

I will respond to your message as soon as possible upon my return.
```

## Disabling Vacation Mode

1. Go to **Settings** > **Vacation**.
2. Toggle **Enable Vacation Reply** to **Off**.

The auto-reply stops immediately. If you set an end date, it also stops automatically at midnight on that date.

## Important Notes

- Auto-replies are sent to a given sender at most once per day by default, even if they send multiple messages.
- Most mailing list and automated messages do not trigger vacation replies.
- Spam messages do not trigger vacation replies.
- If [mail forwarding](forwarding.md) is enabled, auto-replies apply to forwarded mail as well.
- Admin and Operator users can configure vacation replies for other mailboxes from the domain management dashboard.
