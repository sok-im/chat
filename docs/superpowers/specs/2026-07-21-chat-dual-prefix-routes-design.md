# Chat API Dual Prefix Routes Design

## Goal

Make all existing chat HTTP routes reachable under both:

- `/...` (current root, e.g. `POST /account/login`)
- `/chat/v1/...` (prefixed, e.g. `POST /chat/v1/account/login`)

Handlers, middleware, and request/response contracts stay unchanged.

## Approach

Register the same route tree twice by calling `SetChatRoute` on two Gin routers:

1. Root engine: `engine`
2. Prefixed group: `engine.Group("/chat/v1")`

`SetChatRoute` already accepts `gin.IRouter`, so no route definitions need to be duplicated or rewritten.

## Change Location

File: `internal/api/chat/start.go`

In `Start`, replace the single registration:

```go
SetChatRoute(engine, adminApi, mwApi)
```

with:

```go
SetChatRoute(engine, adminApi, mwApi)
SetChatRoute(engine.Group("/chat/v1"), adminApi, mwApi)
```

`SetChatRoute` itself is left as-is.

## Out of Scope

- Admin API routes (`internal/api/admin`)
- Gateway/nginx rewrite rules
- Changing path segments inside `SetChatRoute` (e.g. `/account`, `/user`)

## Success Criteria

- Existing root paths continue to work.
- The same handlers respond on `/chat/v1` + original path.
- No handler or middleware logic changes.
