# Creating Email Groups

A group (also called a distribution list) is a single email address that delivers to multiple mailboxes, aliases, or other groups.

## Creating a Group

1. Go to **Domains** and select your domain.
2. Click the **Groups** tab.
3. Click **Create Group**.
4. Configure the group:

| Field | Description |
| ----- | ----------- |
| **Group Address** | The local part of the group email (e.g., `team` for `team@example.com`). |
| **Display Name** | A friendly name shown in the recipient list (e.g., `All Staff`). |
| **Members** | The mailboxes, aliases, or other groups that receive mail sent here. |

5. Click **Create**.

## Adding and Removing Members

1. Click the group in the **Groups** tab.
2. In the **Members** section, add or remove entries.
3. Click **Save**.

Changes take effect immediately for all new messages.

## Group Visibility

You can control who can send to a group:

| Setting | Description |
| ------- | ----------- |
| **Anyone** | Anyone (internal or external) can email the group. |
| **Organization only** | Only members of your Orvix organization can send to the group. |
| **Members only** | Only group members can send to the group. |
| **Specific addresses** | Restrict to a list of approved email addresses. |

Configure this when creating the group or under the group's **Settings** tab.

## Reply Behavior

By default, replies to group emails go to the original sender, not the entire group. To change this:

1. Edit the group settings.
2. Toggle **Replies go to group** to change where replies are directed.

## Nested Groups

Groups can contain other groups. For example, a group `all@example.com` can contain `engineering@example.com` and `sales@example.com`.

Be careful with deeply nested groups, as this can create circular loops. Orvix detects and prevents direct circular references.

## Deleting a Group

1. Click the group and select **Delete Group**.
2. Confirm the deletion.

Deleting a group does not delete member mailboxes or the emails they have already received.
