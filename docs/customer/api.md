# Public API Overview

The Orvix API lets you manage your organization, domains, mailboxes, and more programmatically.

## API Base URL

```
https://api.orvix.com/v1
```

All requests must use HTTPS.

## Authentication

All API requests require an [API key](api-keys.md). Include it in the `Authorization` header:

```
Authorization: Bearer ovx_sk_your_api_key
```

## Request and Response Format

The API accepts and returns JSON. Set the `Content-Type` header for `POST` and `PUT` requests:

```
Content-Type: application/json
```

## Rate Limits

| Plan | Requests per minute |
| ---- | ------------------: |
| Starter | 60 |
| Professional | 300 |
| Business | 1,000 |
| Enterprise | 5,000 |

Rate limit headers are included in every response:

| Header | Description |
| ------ | ----------- |
| `X-RateLimit-Limit` | Requests allowed per minute. |
| `X-RateLimit-Remaining` | Requests remaining in the current window. |
| `X-RateLimit-Reset` | Unix timestamp when the window resets. |

## Pagination

List endpoints support cursor-based pagination:

```
GET /v1/mailboxes?limit=50&cursor=abc123
```

| Parameter | Description |
| --------- | ----------- |
| `limit` | Maximum items per page (default 25, max 100). |
| `cursor` | Pagination cursor from the previous response. |

Responses include a `next_cursor` field for fetching the next page. When `next_cursor` is `null`, you have reached the end.

## Key Endpoints

| Endpoint | Description |
| -------- | ----------- |
| `GET /v1/domains` | List all domains. |
| `POST /v1/domains` | Add a new domain. |
| `GET /v1/domains/:id` | Get domain details and DNS status. |
| `POST /v1/domains/:id/verify` | Trigger DNS re-check. |
| `GET /v1/domains/:id/mailboxes` | List mailboxes on a domain. |
| `POST /v1/domains/:id/mailboxes` | Create a mailbox. |
| `PUT /v1/mailboxes/:id` | Update a mailbox. |
| `DELETE /v1/mailboxes/:id` | Delete a mailbox. |
| `GET /v1/domains/:id/aliases` | List aliases. |
| `POST /v1/domains/:id/aliases` | Create an alias. |
| `GET /v1/domains/:id/groups` | List groups. |
| `POST /v1/domains/:id/groups` | Create a group. |
| `GET /v1/members` | List organization members. |
| `POST /v1/members/invite` | Invite a new member. |
| `GET /v1/usage` | Retrieve current usage metrics. |

## Error Responses

The API uses standard HTTP status codes:

| Code | Meaning |
| ---- | ------- |
| 200 | Success. |
| 201 | Resource created. |
| 400 | Bad request — check your parameters. |
| 401 | Invalid or missing API key. |
| 403 | Insufficient permissions for the requested action. |
| 404 | Resource not found. |
| 429 | Rate limit exceeded. Retry after the reset window. |
| 500 | Server error. Retry or contact support. |

Error response body:

```json
{
  "error": {
    "code": "invalid_parameter",
    "message": "The 'email' field is required.",
    "details": {
      "field": "email"
    }
  }
}
```

## Full API Reference

Complete endpoint documentation with request/response schemas is available at [https://docs.orvix.com/api](https://docs.orvix.com/api).
