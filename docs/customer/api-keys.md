# Creating and Managing API Keys

API keys allow programmatic access to your Orvix organization. Use them to automate tasks, integrate with other systems, or build custom tools.

## Creating an API Key

1. Sign in to your Orvix account.
2. Go to **Account Settings** > **API Keys**.
3. Click **Create API Key**.
4. Enter a **name** to identify the key (e.g., "Deployment Script", "Monitoring Bot").
5. Select the [role](roles.md) that defines the key's permissions. Consider using a dedicated role with limited permissions.
6. Click **Create**.

**Important**: The API key is shown only once. Copy and store it securely. If you lose it, you must create a new key.

## API Key Format

Orvix API keys follow this format:

```
ovx_sk_a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6
```

Include the key in API requests via the `Authorization` header:

```
Authorization: Bearer ovx_sk_a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6
```

## Managing API Keys

The **API Keys** page lists all your keys:

- **Name**: The friendly name you assigned.
- **Created**: When the key was created.
- **Last used**: The last time the key was used in an API request.
- **Status**: Active or revoked.

## Revoking a Key

1. Go to **Account Settings** > **API Keys**.
2. Find the key and click **Revoke**.
3. Confirm the action.

Revocation is immediate. Any application using the revoked key will begin receiving `401 Unauthorized` responses.

## Best Practices

- Create separate keys for each application or script.
- Use the most restrictive role possible. Never assign Owner-level permissions unless absolutely necessary.
- Rotate keys periodically. Create a new key, update your application, then revoke the old one.
- Never embed API keys in client-side code, public repositories, or logs.
- Set up alerts for unusual API activity from **Account Settings** > **Notifications**.

## Rate Limits

API keys are subject to the same [rate limits](api.md) as all API requests. Check the [API documentation](api.md) for details on limits per endpoint and plan.
