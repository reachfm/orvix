# Adding Email Signatures

An email signature is a block of text, images, and links automatically appended to your outgoing messages.

## Creating a Signature

1. Sign in to [Orvix Webmail](webmail.md).
2. Click the gear icon (**Settings**) in the top-right corner.
3. Go to **Signatures**.
4. Click **Create Signature**.
5. Enter a **name** for the signature (e.g., "Work Default").
6. Compose your signature using the rich text editor.
7. Click **Save**.

## Signature Editor Features

The rich text editor supports:

- Bold, italic, and underlined text.
- Font size and color selection.
- Links and images.
- Horizontal lines and dividers.
- Variable placeholders: `{{display_name}}`, `{{email}}`, `{{phone}}`, `{{company}}`.

## Assigning Signatures

After creating a signature, assign it:

| Setting | Description |
| ------- | ----------- |
| **Default for new messages** | Automatically inserts this signature when composing new emails. |
| **Default for replies/forwards** | Automatically inserts this signature when replying or forwarding. |

You can create multiple signatures and select them from the compose window dropdown.

## Signature Best Practices

- Keep it concise. Include name, title, company, and one or two contact methods.
- Use a small logo (less than 100px tall) to avoid clipping in replies.
- Avoid large images, animated GIFs, and heavy HTML.
- Test your signature by sending an email to yourself first.
- For legal disclaimers, consider using organization-level signatures (admins configure these under **Organization** > **Settings** > **Signatures**).

## Organization-Level Signatures

Admins can set mandatory signatures for all outbound email from a domain:

1. Go to **Organization** > **Settings** > **Signatures**.
2. Create a signature and enable **Apply to all mailboxes**.
3. The signature is appended after the user's personal signature.

## Sample Signature

```
John Doe
Engineering Lead | Orvix Corp
john.doe@example.com | (555) 123-4567
https://example.com
```
