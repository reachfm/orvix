# Setting Up Mail Rules and Filters

Mail rules automatically process incoming and outgoing messages based on conditions you define.

## Accessing Rules

1. Sign in to [Orvix Webmail](webmail.md).
2. Click the gear icon (**Settings**) in the top-right corner.
3. Go to **Filters & Rules**.

## Creating a Rule

1. Click **Create Rule**.
2. Give your rule a descriptive name (e.g., "Move invoices to folder").
3. Define one or more **conditions**:

| Condition | Example |
| --------- | ------- |
| From contains | `billing@vendor.com` |
| Subject contains | `Invoice` |
| To/Cc contains | `billing@example.com` |
| Has attachment | Yes/No |
| Message size greater than | 10 MB |

4. If you add multiple conditions, choose whether **All** or **Any** must match.

5. Define the **action** to perform:

| Action | Description |
| ------ | ----------- |
| Move to folder | Move to a specific folder (e.g., `Invoices`). |
| Mark as read | Mark the message as read. |
| Mark as important | Flag as important. |
| Forward to | Forward a copy to another address. |
| Delete | Send directly to Trash. |
| Add label | Apply a color-coded label. |

6. Click **Save**.

## Rule Order

Rules are processed top to bottom. Drag and drop rules to reorder them. Once a rule matches and processes a message, subsequent rules are not applied by default. Enable **Continue processing** on a rule to allow subsequent rules to run.

## Common Use Cases

- **Client organization**: Move messages from `client.com` to a client-specific folder.
- **Newsletter sorting**: Move messages from mailing lists to a "Newsletters" folder and mark as read.
- **Priority alerts**: Flag messages from managers or critical addresses.
- **Spam reduction**: Delete or move messages containing specific spam phrases.
- **Attachment management**: Move large attachments to a designated folder.

## Managing Rules

- **Toggle** a rule on or off using the switch next to it.
- **Edit** a rule by clicking on it and modifying conditions or actions.
- **Delete** a rule permanently from the rule list.

Rules apply only to new messages received after the rule is created. They do not retroactively process existing messages.
