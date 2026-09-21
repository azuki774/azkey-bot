# Misskey HTTP client scope

The shared client in `internal/misskey` targets the upstream Misskey tag
[`2026.9.0`](https://github.com/misskey-dev/misskey/tree/2026.9.0). The source
links below were checked at that tag on 2026-09-21. This is source verification
only; no live Misskey server was contacted.

## Supported endpoints

All calls use `POST /api/{endpoint}` with a JSON body. The token is sent in the
`i` property, matching the [`misskey-js` API client](https://raw.githubusercontent.com/misskey-dev/misskey/2026.9.0/packages/misskey-js/src/api.ts).
The endpoint request and response catalog is generated in
[`misskey-js` at this tag](https://raw.githubusercontent.com/misskey-dev/misskey/2026.9.0/packages/misskey-js/src/autogen/endpoint.ts).
The client implements one page per method and does not fetch subsequent pages
automatically.

| Client method | Misskey endpoint | Verified source details |
| --- | --- | --- |
| `Self` | [`i`](https://raw.githubusercontent.com/misskey-dev/misskey/2026.9.0/packages/backend/src/server/api/endpoints/i.ts) | Requires a credential and returns `MeDetailed`. |
| `ListFollowers` | [`users/followers`](https://raw.githubusercontent.com/misskey-dev/misskey/2026.9.0/packages/backend/src/server/api/endpoints/users/followers.ts) | Requires `userId`; supports `sinceId`, `untilId`, and `limit` from 1 through 100, with a default of 10. The cursors are `following.id` relationship IDs. |
| `ListFollowing` | [`users/following`](https://raw.githubusercontent.com/misskey-dev/misskey/2026.9.0/packages/backend/src/server/api/endpoints/users/following.ts) | Requires `userId`; supports the same relationship pagination fields. The cursors are relationship IDs, not user IDs. |
| `ListUserNotes` | [`users/notes`](https://raw.githubusercontent.com/misskey-dev/misskey/2026.9.0/packages/backend/src/server/api/endpoints/users/notes.ts) | Requires `userId`; supports `sinceId`, `untilId`, and `limit` from 1 through 100, with a default of 10. Cursors are note IDs. `text` and `cw` are nullable in the [generated Note model](https://raw.githubusercontent.com/misskey-dev/misskey/2026.9.0/packages/misskey-js/src/autogen/types.ts). |
| `CreateFollow` | [`following/create`](https://raw.githubusercontent.com/misskey-dev/misskey/2026.9.0/packages/backend/src/server/api/endpoints/following/create.ts) | Requires `userId`, returns `UserLite`, and declares a 100 requests/hour endpoint limit. |
| `CreateReaction` | [`notes/reactions/create`](https://raw.githubusercontent.com/misskey-dev/misskey/2026.9.0/packages/backend/src/server/api/endpoints/notes/reactions/create.ts) | Requires `noteId` and `reaction`, returns no response body, and has no endpoint `limit` block in the checked source. |

Reaction deletion is intentionally not implemented. Other Misskey endpoints,
date cursors, all-pages helpers, streaming, retries, and backoff are outside
this scope.

## Credentials and pagination details

The endpoint metadata at this tag declares `read:account` for [`i`](https://raw.githubusercontent.com/misskey-dev/misskey/2026.9.0/packages/backend/src/server/api/endpoints/i.ts), `write:following` for [`following/create`](https://raw.githubusercontent.com/misskey-dev/misskey/2026.9.0/packages/backend/src/server/api/endpoints/following/create.ts), and `write:reactions` for [`notes/reactions/create`](https://raw.githubusercontent.com/misskey-dev/misskey/2026.9.0/packages/backend/src/server/api/endpoints/notes/reactions/create.ts). The list endpoints do not declare a permission kind and can be public, but this client still sends its configured token. The client leaves the returned Misskey error code available for the consumer; it does not turn endpoint-specific outcomes into retries.

For the relationship endpoints, [`QueryService.makePaginationQuery`](https://raw.githubusercontent.com/misskey-dev/misskey/2026.9.0/packages/backend/src/core/QueryService.ts) uses exclusive bounds: `id > sinceId` and `id < untilId`. With only `sinceId`, results are ascending; with only `untilId`, with both cursors, or with neither cursor, results are descending. These IDs are the relationship record IDs returned as `Following.id`, not the user IDs.

`users/notes` keeps the source defaults: `withReplies=false`,
`withRenotes=true`, and `withChannelNotes=false`. Its order follows the
[`FanoutTimelineEndpointService`](https://raw.githubusercontent.com/misskey-dev/misskey/2026.9.0/packages/backend/src/core/FanoutTimelineEndpointService.ts): since-only is ascending and the other cursor combinations are descending; the database fallback uses the same `QueryService` rule. The client does not sort responses. Misskey can remove notes through visibility, mute, block, channel, reply, and renote filters, so a short page alone cannot prove that a cursor range is complete. Public-post selection remains a later roumu polling concern.

`following/create` can return source-defined errors such as
`NO_SUCH_USER`, `FOLLOWEE_IS_YOURSELF`, `ALREADY_FOLLOWING`, `BLOCKING`, and
`BLOCKED`; a locked or careful account can instead create a follow request in
[`UserFollowingService`](https://raw.githubusercontent.com/misskey-dev/misskey/2026.9.0/packages/backend/src/core/UserFollowingService.ts). API errors retain their codes for the later #13 use case. A successful response does not necessarily mean the follow request has already been accepted; this client does not distinguish pending requests from completed follows.

## Response and error behavior

Misskey's API server emits HTTP 429 with error code `RATE_LIMIT_EXCEEDED` and,
when rate-limit reset information is available, a `Retry-After` header. This is
defined in [`ApiCallService`](https://raw.githubusercontent.com/misskey-dev/misskey/2026.9.0/packages/backend/src/server/api/ApiCallService.ts).
The client classifies every HTTP 429 as a rate-limit error and exposes a parsed
`RetryAfter` duration only when that header is present and valid. It does not
infer a delay from an error body and does not retry requests. The upstream
source writes `Retry-After` as decimal seconds, so this client intentionally
supports that form only.

HTTP errors are converted to the shared `internal/domain.Error` type. Its
classification covers authentication, client, rate-limit, server, network,
network-timeout, cancellation, and invalid-response failures. Error strings do
not include response bodies, URLs, tokens, or underlying transport messages.
Responses are bounded, and redirects are returned rather than followed so a
request body containing a token cannot be forwarded to another host.
