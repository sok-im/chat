# Chat Dual Prefix Routes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Register all existing chat HTTP routes under both `/` and `/chat/v1` so clients can call either path prefix.

**Architecture:** Keep `SetChatRoute` unchanged. In `Start`, call it once on the root Gin engine and once on `engine.Group("/chat/v1")`. Add a focused route-registration test that asserts both prefixes exist for representative paths.

**Tech Stack:** Go, Gin (`github.com/gin-gonic/gin`)

## Global Constraints

- Dual prefixes only for chat API (`internal/api/chat`); admin API out of scope
- Do not change handler or middleware logic inside `SetChatRoute`
- Prefixed base path is exactly `/chat/v1`
- Prefer minimal diff: one registration call + one test file

## File Structure

| File | Responsibility |
|------|----------------|
| `internal/api/chat/start.go` | `Start` mounts chat routes on root and `/chat/v1` |
| `internal/api/chat/start.go` (`SetChatRoute`) | Unchanged route tree definition |
| `internal/api/chat/route_prefix_test.go` | Asserts dual-prefix registration for sample routes |

---

### Task 1: Dual-prefix route registration

**Files:**
- Modify: `internal/api/chat/start.go` (around the `SetChatRoute(engine, adminApi, mwApi)` call in `Start`)
- Create: `internal/api/chat/route_prefix_test.go`
- Test: `internal/api/chat/route_prefix_test.go`

**Interfaces:**
- Consumes: existing `SetChatRoute(router gin.IRouter, chat *Api, mw *chatmw.MW)`
- Produces: routes available at both `/...` and `/chat/v1/...`

- [ ] **Step 1: Write the failing test**

Create `internal/api/chat/route_prefix_test.go`:

```go
package chat

import (
	"testing"

	"github.com/gin-gonic/gin"
	chatmw "github.com/openimsdk/chat/internal/api/mw"
)

func TestChatRoutesDualPrefix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()

	api := &Api{}
	mw := &chatmw.MW{}
	SetChatRoute(engine, api, mw)
	SetChatRoute(engine.Group("/chat/v1"), api, mw)

	want := []string{
		"POST /account/login",
		"POST /chat/v1/account/login",
		"POST /user/update",
		"POST /chat/v1/user/update",
		"POST /totp/verify",
		"POST /chat/v1/totp/verify",
	}

	have := make(map[string]bool, len(engine.Routes()))
	for _, r := range engine.Routes() {
		have[r.Method+" "+r.Path] = true
	}
	for _, route := range want {
		if !have[route] {
			t.Errorf("missing route %s", route)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
cd /Users/lintao/important/ai-customer/openim/chat && go test ./internal/api/chat/ -run TestChatRoutesDualPrefix -v
```

Expected: FAIL with `missing route POST /chat/v1/account/login` (and the other `/chat/v1/...` routes), because `Start` does not yet double-register and this test currently encodes the intended dual call—after Step 1 alone, the test already double-calls `SetChatRoute`, so it would pass early.

To keep a real red→green cycle against production wiring, temporarily have the test call only the single registration that production uses today:

```go
	SetChatRoute(engine, api, mw)
	// dual prefix not yet applied in Start; assert production gap:
```

Replace the dual calls in Step 1 temporarily with a single `SetChatRoute(engine, api, mw)` only, then run the same command.

Expected: FAIL:

```
missing route POST /chat/v1/account/login
```

- [ ] **Step 3: Write minimal implementation**

In `internal/api/chat/start.go`, inside `Start`, change:

```go
	SetChatRoute(engine, adminApi, mwApi)
```

to:

```go
	SetChatRoute(engine, adminApi, mwApi)
	SetChatRoute(engine.Group("/chat/v1"), adminApi, mwApi)
```

Then update the test to mirror production (dual registration):

```go
	SetChatRoute(engine, api, mw)
	SetChatRoute(engine.Group("/chat/v1"), api, mw)
```

Do not modify `SetChatRoute` body.

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
cd /Users/lintao/important/ai-customer/openim/chat && go test ./internal/api/chat/ -run TestChatRoutesDualPrefix -v
```

Expected: PASS (`ok` / `PASS`)

Also confirm package builds:

```bash
cd /Users/lintao/important/ai-customer/openim/chat && go build ./internal/api/chat/
```

Expected: exit code 0, no output

- [ ] **Step 5: Commit**

```bash
git add internal/api/chat/start.go internal/api/chat/route_prefix_test.go docs/superpowers/specs/2026-07-21-chat-dual-prefix-routes-design.md docs/superpowers/plans/2026-07-21-chat-dual-prefix-routes.md
git commit -m "$(cat <<'EOF'
feat(chat-api): register routes under / and /chat/v1

Allow clients to hit the same handlers with either path prefix.
EOF
)"
```

Only commit if the user explicitly requested a commit in this session; otherwise stop after Step 4 and report status.
