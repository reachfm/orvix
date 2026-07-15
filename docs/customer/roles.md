# Understanding Roles

Orvix uses role-based access control (RBAC) to manage what each team member can do within your organization.

## Available Roles

| Role | Description |
| ---- | ----------- |
| **Owner** | Full control over the organization. Only one owner per organization. Can transfer ownership. |
| **Admin** | Full management access. Can manage domains, mailboxes, members, billing, and settings. Cannot delete the organization or transfer ownership. |
| **Operator** | Day-to-day management. Can manage mailboxes, aliases, groups, and view domain settings. Cannot manage members, billing, or organization security settings. |
| **Read-Only** | View-only access. Can see domains, mailboxes, and usage reports. Cannot make changes. |
| **User** | A standard mailbox user with access to webmail and personal settings only. Cannot access organization management features. |

## Role Permissions at a Glance

| Permission | Owner | Admin | Operator | Read-Only | User |
| ---------- | :---: | :---: | :------: | :-------: | :-: |
| Manage members & invites | ✓ | ✓ | ✗ | ✗ | ✗ |
| Manage billing & subscription | ✓ | ✓ | ✗ | ✗ | ✗ |
| Manage domains | ✓ | ✓ | ✓ | ✗ | ✗ |
| Manage mailboxes | ✓ | ✓ | ✓ | ✗ | ✗ |
| Manage aliases & groups | ✓ | ✓ | ✓ | ✗ | ✗ |
| Manage security settings | ✓ | ✓ | ✗ | ✗ | ✗ |
| View organization data | ✓ | ✓ | ✓ | ✓ | ✗ |
| Delete organization | ✓ | ✗ | ✗ | ✗ | ✗ |
| Transfer ownership | ✓ | ✗ | ✗ | ✗ | ✗ |
| Access webmail | ✓ | ✓ | ✓ | ✓ | ✓ |

## Changing a Member's Role

1. Go to **Organization** > **Members**.
2. Find the member whose role you want to change.
3. Click the role dropdown and select the new role.
4. The change takes effect immediately.
