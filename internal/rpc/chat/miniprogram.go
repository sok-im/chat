package chat

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/openimsdk/tools/errs"

	"github.com/openimsdk/chat/pkg/common/db/cache"
	"github.com/openimsdk/chat/pkg/common/db/dbutil"
	chatdb "github.com/openimsdk/chat/pkg/common/db/table/chat"
	"github.com/openimsdk/chat/pkg/eerrs"
	"github.com/openimsdk/chat/pkg/protocol/chat"
)

const (
	mpEntryTypeFinclip = "finclip"
	mpEntryTypeDapp    = "dapp"
	mpStatusOnline     = "online"

	mpDefaultTicketTTL  = 300
	mpMaxTicketTTL      = 900
	mpDefaultLaunchTTL  = 300
	mpDefaultCacheTTL   = 600
	mpMaxRecent         = 50
	mpDefaultRecent     = 20
	mpMaxFavorites      = 100
	mpMaxCatalogLimit   = 100
	mpDefaultCatalog    = 50
	mpMaxEvents         = 50
	mpDefaultScopeRead  = "profile:read"
	mpTokenTypeTicket   = "SOK-MP-Ticket"
	mpSubjectHashLength = 24
)

var mpEventTypeWhitelist = map[string]struct{}{
	"sdk_init":             {},
	"launch_accepted":      {},
	"applet_opened":        {},
	"applet_closed":        {},
	"open_failed":          {},
	"extension_api_called": {},
}

// ===================== Catalog =====================

func (o *chatSvr) MiniProgramCatalog(ctx context.Context, req *chat.MiniProgramCatalogReq) (*chat.MiniProgramCatalogResp, error) {
	resp := &chat.MiniProgramCatalogResp{
		Categories: []*chat.MiniProgramCategory{},
		Items:      []*chat.MiniProgramEntry{},
	}
	if !o.MiniProgram.Enabled || !o.MiniProgram.CatalogEnabled {
		resp.Etag = o.catalogETag(req.UserID, 0)
		return resp, nil
	}
	entries, revision, err := o.Database.MiniProgramFindOnlineEntries(ctx, req.CategoryId, strings.TrimSpace(req.Keyword))
	if err != nil {
		return nil, err
	}
	categories, err := o.Database.MiniProgramFindCategories(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range categories {
		resp.Categories = append(resp.Categories, &chat.MiniProgramCategory{Id: c.ID, Name: c.Name, Sort: c.Sort})
	}
	limit := int(req.Limit)
	if limit <= 0 {
		limit = mpDefaultCatalog
	}
	if limit > mpMaxCatalogLimit {
		limit = mpMaxCatalogLimit
	}
	for _, e := range entries {
		if !mpPlatformAllowed(e.Platforms, req.Platform) {
			continue
		}
		if len(resp.Items) >= limit {
			break
		}
		resp.Items = append(resp.Items, mpEntryToProto(e))
	}
	resp.Revision = revision
	resp.Etag = o.catalogETag(req.UserID, revision)
	return resp, nil
}

// ===================== Launch =====================

func (o *chatSvr) MiniProgramLaunch(ctx context.Context, req *chat.MiniProgramLaunchReq) (*chat.MiniProgramLaunchResp, error) {
	if req.EntryId == "" {
		return nil, eerrs.ErrMpValidationFailed.WrapMsg("entryId required")
	}
	entry, err := o.mpTakeEntry(ctx, req.EntryId)
	if err != nil {
		return nil, err
	}
	if entry.Status != mpStatusOnline {
		return nil, eerrs.ErrMpEntrySuspended.WrapMsg("entry not online")
	}
	if !mpPlatformAllowed(entry.Platforms, req.Platform) {
		return nil, eerrs.ErrMpForbidden.WrapMsg("platform not supported")
	}
	if entry.MinAppVersion != "" && req.ClientVersion != "" && compareSemver(req.ClientVersion, entry.MinAppVersion) < 0 {
		return nil, eerrs.ErrMpVersionTooLow.WrapMsg("client version too low")
	}
	resp := &chat.MiniProgramLaunchResp{
		EntryId:      entry.ID,
		Revision:     entry.Revision,
		EntryType:    entry.EntryType,
		ExpiresAt:    mpFormatTime(time.Now().Add(time.Duration(o.mpLaunchTTL()) * time.Second)),
		StaleCatalog: req.CatalogRevision != 0 && req.CatalogRevision < entry.Revision,
	}
	switch entry.EntryType {
	case mpEntryTypeFinclip:
		if entry.FinclipAppID == "" {
			return nil, eerrs.ErrMpInternal.WrapMsg("finclip entry missing appId")
		}
		resp.Finclip = mpFinclipToProto(entry)
	case mpEntryTypeDapp:
		if entry.DappURL == "" {
			return nil, eerrs.ErrMpInternal.WrapMsg("dapp entry missing url")
		}
		resp.Dapp = &chat.MiniProgramDapp{Url: entry.DappURL, Title: entry.DappTitle}
	default:
		return nil, eerrs.ErrMpInternal.WrapMsg("unknown entryType")
	}
	return resp, nil
}

// ===================== Session issue =====================

func (o *chatSvr) MiniProgramSessionIssue(ctx context.Context, req *chat.MiniProgramSessionIssueReq) (*chat.MiniProgramSessionIssueResp, error) {
	if req.UserID == "" {
		return nil, eerrs.ErrMpUnauthenticated.WrapMsg("login required")
	}
	if req.EntryId == "" || req.FinclipAppId == "" || req.InstallId == "" {
		return nil, eerrs.ErrMpValidationFailed.WrapMsg("entryId, finclipAppId and installId required")
	}
	if req.IdempotencyKey != "" {
		if data, ok, err := o.MiniProgramCache.GetIdempotent(ctx, req.UserID, req.IdempotencyKey); err != nil {
			return nil, err
		} else if ok {
			var cached chat.MiniProgramSessionIssueResp
			if err := json.Unmarshal(data, &cached); err == nil {
				return &cached, nil
			}
		}
	}
	entry, err := o.mpTakeEntry(ctx, req.EntryId)
	if err != nil {
		return nil, err
	}
	if entry.Status != mpStatusOnline {
		return nil, eerrs.ErrMpEntrySuspended.WrapMsg("entry not online")
	}
	if entry.EntryType != mpEntryTypeFinclip {
		return nil, eerrs.ErrMpValidationFailed.WrapMsg("session only for finclip entry")
	}
	if entry.FinclipAppID != req.FinclipAppId {
		return nil, eerrs.ErrMpForbidden.WrapMsg("finclipAppId mismatch")
	}
	scope := mpEffectiveScope(req.RequestedScope, entry.Scopes)
	ttl := o.mpTicketTTL()
	jti, err := mpRandomToken()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	claims := &cache.MiniProgramTicketClaims{
		JTI:          jti,
		UserID:       req.UserID,
		FinclipAppID: entry.FinclipAppID,
		EntryID:      entry.ID,
		InstallID:    mpHash(req.InstallId),
		Scope:        scope,
		IssuedAt:     now.Unix(),
		ExpiresAt:    now.Add(time.Duration(ttl) * time.Second).Unix(),
	}
	if err := o.MiniProgramCache.SetTicket(ctx, claims, time.Duration(ttl)*time.Second); err != nil {
		return nil, err
	}
	resp := &chat.MiniProgramSessionIssueResp{
		Ticket:       jti,
		TokenType:    mpTokenTypeTicket,
		ExpiresIn:    int32(ttl),
		Scope:        scope,
		RefreshAfter: int32(ttl * 4 / 5),
	}
	if req.IdempotencyKey != "" {
		if data, err := json.Marshal(resp); err == nil {
			_ = o.MiniProgramCache.SetIdempotent(ctx, req.UserID, req.IdempotencyKey, data)
		}
	}
	return resp, nil
}

// ===================== Session introspect (internal) =====================

func (o *chatSvr) MiniProgramSessionIntrospect(ctx context.Context, req *chat.MiniProgramSessionIntrospectReq) (*chat.MiniProgramSessionIntrospectResp, error) {
	if req.Token == "" {
		return &chat.MiniProgramSessionIntrospectResp{Active: false}, nil
	}
	claims, err := o.MiniProgramCache.GetTicket(ctx, req.Token)
	if err != nil {
		return nil, err
	}
	if claims == nil {
		return &chat.MiniProgramSessionIntrospectResp{Active: false}, nil
	}
	if time.Now().Unix() >= claims.ExpiresAt {
		return &chat.MiniProgramSessionIntrospectResp{Active: false}, nil
	}
	if req.ExpectedAppId != "" && claims.FinclipAppID != req.ExpectedAppId {
		return &chat.MiniProgramSessionIntrospectResp{Active: false}, nil
	}
	return &chat.MiniProgramSessionIntrospectResp{
		Active:       true,
		Subject:      o.mpSubject(claims.UserID, claims.FinclipAppID),
		FinclipAppId: claims.FinclipAppID,
		EntryId:      claims.EntryID,
		Scope:        claims.Scope,
		ExpiresAt:    mpFormatTime(time.Unix(claims.ExpiresAt, 0)),
	}, nil
}

// ===================== Session revoke (internal) =====================

func (o *chatSvr) MiniProgramSessionRevoke(ctx context.Context, req *chat.MiniProgramSessionRevokeReq) (*chat.MiniProgramSessionRevokeResp, error) {
	if req.UserID == "" && req.EntryId == "" && req.FinclipAppId == "" && req.Jti == "" {
		return nil, eerrs.ErrMpValidationFailed.WrapMsg("at least one revoke dimension required")
	}
	count, err := o.MiniProgramCache.RevokeTickets(ctx, req.UserID, req.InstallId, req.EntryId, req.FinclipAppId, req.Jti)
	if err != nil {
		return nil, err
	}
	return &chat.MiniProgramSessionRevokeResp{
		RevokedCount: count,
		NotBefore:    mpFormatTime(time.Now()),
	}, nil
}

// ===================== Recent =====================

func (o *chatSvr) MiniProgramGetRecent(ctx context.Context, req *chat.MiniProgramGetRecentReq) (*chat.MiniProgramGetRecentResp, error) {
	resp := &chat.MiniProgramGetRecentResp{Items: []*chat.MiniProgramRecentItem{}}
	limit := int64(req.Limit)
	if limit <= 0 {
		limit = mpDefaultRecent
	}
	if limit > mpMaxRecent {
		limit = mpMaxRecent
	}
	records, err := o.Database.MiniProgramFindRecent(ctx, req.UserID, limit)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return resp, nil
	}
	ids := make([]string, 0, len(records))
	for _, r := range records {
		ids = append(ids, r.EntryID)
	}
	entryMap, err := o.mpEntryMap(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, r := range records {
		entry, ok := entryMap[r.EntryID]
		if !ok || entry.Status != mpStatusOnline {
			continue
		}
		resp.Items = append(resp.Items, &chat.MiniProgramRecentItem{
			EntryId:       r.EntryID,
			LastOpenedAt:  mpFormatTime(r.LastOpenedAt),
			EntrySnapshot: mpSnapshot(entry),
		})
	}
	return resp, nil
}

func (o *chatSvr) MiniProgramPutRecent(ctx context.Context, req *chat.MiniProgramPutRecentReq) (*chat.MiniProgramPutRecentResp, error) {
	if req.EntryId == "" {
		return nil, eerrs.ErrMpValidationFailed.WrapMsg("entryId required")
	}
	entry, err := o.mpTakeEntry(ctx, req.EntryId)
	if err != nil {
		return nil, err
	}
	if entry.Status != mpStatusOnline {
		return nil, eerrs.ErrMpEntrySuspended.WrapMsg("entry not online")
	}
	now := time.Now()
	total, err := o.Database.MiniProgramUpsertRecent(ctx, &chatdb.MiniProgramRecentEntry{
		UserID:       req.UserID,
		EntryID:      req.EntryId,
		LastOpenedAt: now,
	}, mpMaxRecent)
	if err != nil {
		return nil, err
	}
	return &chat.MiniProgramPutRecentResp{
		EntryId:      req.EntryId,
		LastOpenedAt: mpFormatTime(now),
		Total:        total,
	}, nil
}

// ===================== Favorites =====================

func (o *chatSvr) MiniProgramGetFavorites(ctx context.Context, req *chat.MiniProgramGetFavoritesReq) (*chat.MiniProgramGetFavoritesResp, error) {
	resp := &chat.MiniProgramGetFavoritesResp{
		EntryIds: []string{},
		Items:    []*chat.MiniProgramFavoriteItem{},
	}
	fav, err := o.Database.MiniProgramTakeFavorite(ctx, req.UserID)
	if err != nil {
		if dbutil.IsDBNotFound(err) {
			return resp, nil
		}
		return nil, err
	}
	resp.Revision = fav.Revision
	resp.EntryIds = fav.EntryIDs
	entryMap, err := o.mpEntryMap(ctx, fav.EntryIDs)
	if err != nil {
		return nil, err
	}
	for _, id := range fav.EntryIDs {
		item := &chat.MiniProgramFavoriteItem{EntryId: id}
		if entry, ok := entryMap[id]; ok {
			item.Available = entry.Status == mpStatusOnline
			item.EntrySnapshot = mpSnapshot(entry)
		}
		resp.Items = append(resp.Items, item)
	}
	return resp, nil
}

func (o *chatSvr) MiniProgramPutFavorites(ctx context.Context, req *chat.MiniProgramPutFavoritesReq) (*chat.MiniProgramPutFavoritesResp, error) {
	if len(req.EntryIds) > mpMaxFavorites {
		return nil, eerrs.ErrMpValidationFailed.WrapMsg("too many favorites")
	}
	var currentRev int64
	var currentIDs []string
	fav, err := o.Database.MiniProgramTakeFavorite(ctx, req.UserID)
	if err != nil {
		if !dbutil.IsDBNotFound(err) {
			return nil, err
		}
	} else {
		currentRev = fav.Revision
		currentIDs = fav.EntryIDs
	}
	if req.Revision != currentRev {
		return &chat.MiniProgramPutFavoritesResp{
			Revision: currentRev,
			EntryIds: currentIDs,
			Conflict: true,
		}, nil
	}
	entryIDs := mpDedup(req.EntryIds)
	if len(entryIDs) > 0 {
		existing, err := o.Database.MiniProgramFindEntriesByIDs(ctx, entryIDs)
		if err != nil {
			return nil, err
		}
		existSet := make(map[string]struct{}, len(existing))
		for _, e := range existing {
			existSet[e.ID] = struct{}{}
		}
		for _, id := range entryIDs {
			if _, ok := existSet[id]; !ok {
				return nil, eerrs.ErrMpValidationFailed.WrapMsg("unknown entryId: " + id)
			}
		}
	}
	newRev := currentRev + 1
	if err := o.Database.MiniProgramUpsertFavorite(ctx, &chatdb.MiniProgramFavorite{
		UserID:    req.UserID,
		EntryIDs:  entryIDs,
		Revision:  newRev,
		UpdatedAt: time.Now(),
	}); err != nil {
		return nil, err
	}
	return &chat.MiniProgramPutFavoritesResp{
		Revision: newRev,
		EntryIds: entryIDs,
		Conflict: false,
	}, nil
}

// ===================== Runtime config =====================

func (o *chatSvr) MiniProgramRuntimeConfig(ctx context.Context, req *chat.MiniProgramRuntimeConfigReq) (*chat.MiniProgramRuntimeConfigResp, error) {
	cacheTTL := o.MiniProgram.CacheTTLSeconds
	if cacheTTL <= 0 {
		cacheTTL = mpDefaultCacheTTL
	}
	minVersion := o.MiniProgram.MinClientVersion
	if minVersion == "" {
		minVersion = "1.0.0"
	}
	return &chat.MiniProgramRuntimeConfigResp{
		Enabled:          o.MiniProgram.Enabled,
		CatalogEnabled:   o.MiniProgram.CatalogEnabled,
		AllowOfflineOpen: o.MiniProgram.AllowOfflineOpen,
		MinClientVersion: minVersion,
		CacheTtlSeconds:  int32(cacheTTL),
	}, nil
}

// ===================== Events =====================

func (o *chatSvr) MiniProgramReportEvents(ctx context.Context, req *chat.MiniProgramReportEventsReq) (*chat.MiniProgramReportEventsResp, error) {
	// Event ingestion must never block opening a mini-program: unknown event types
	// are silently dropped and the call always succeeds. Wiring into the analytics
	// pipeline is left to the telemetry channel (out of scope here).
	if len(req.Events) > mpMaxEvents {
		req.Events = req.Events[:mpMaxEvents]
	}
	return &chat.MiniProgramReportEventsResp{}, nil
}

// ===================== helpers =====================

func (o *chatSvr) mpTakeEntry(ctx context.Context, id string) (*chatdb.MiniProgramEntry, error) {
	entry, err := o.Database.MiniProgramTakeEntry(ctx, id)
	if err != nil {
		if dbutil.IsDBNotFound(err) {
			return nil, eerrs.ErrMpEntryNotFound.WrapMsg("entry not found")
		}
		return nil, err
	}
	return entry, nil
}

func (o *chatSvr) mpEntryMap(ctx context.Context, ids []string) (map[string]*chatdb.MiniProgramEntry, error) {
	entries, err := o.Database.MiniProgramFindEntriesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	m := make(map[string]*chatdb.MiniProgramEntry, len(entries))
	for _, e := range entries {
		m[e.ID] = e
	}
	return m, nil
}

func (o *chatSvr) mpTicketTTL() int {
	ttl := o.MiniProgram.TicketTTLSeconds
	if ttl <= 0 {
		ttl = mpDefaultTicketTTL
	}
	if ttl > mpMaxTicketTTL {
		ttl = mpMaxTicketTTL
	}
	return ttl
}

func (o *chatSvr) mpLaunchTTL() int {
	ttl := o.MiniProgram.LaunchTTLSeconds
	if ttl <= 0 {
		ttl = mpDefaultLaunchTTL
	}
	return ttl
}

func (o *chatSvr) catalogETag(userID string, revision int64) string {
	return fmt.Sprintf("\"catalog:u-%s:r-%d\"", mpHash(userID)[:12], revision)
}

// mpSubject derives a pseudonymous, per-app stable subject. It never exposes the
// raw IM userID; identical (userID, appId) pairs always map to the same subject.
func (o *chatSvr) mpSubject(userID, appID string) string {
	h := sha256.Sum256([]byte(o.MiniProgram.TicketSecret + ":" + userID + ":" + appID))
	return "mpu_" + hex.EncodeToString(h[:])[:mpSubjectHashLength]
}

func mpEntryToProto(e *chatdb.MiniProgramEntry) *chat.MiniProgramEntry {
	item := &chat.MiniProgramEntry{
		Id:            e.ID,
		EntryType:     e.EntryType,
		Name:          e.Name,
		Description:   e.Description,
		IconUrl:       e.IconURL,
		CategoryIds:   e.CategoryIDs,
		Permissions:   e.Permissions,
		MinAppVersion: e.MinAppVersion,
		Revision:      e.Revision,
		Sort:          e.Sort,
	}
	if item.CategoryIds == nil {
		item.CategoryIds = []string{}
	}
	if item.Permissions == nil {
		item.Permissions = []string{}
	}
	switch e.EntryType {
	case mpEntryTypeFinclip:
		item.Finclip = mpFinclipToProto(e)
	case mpEntryTypeDapp:
		item.Dapp = &chat.MiniProgramDapp{Url: e.DappURL, Title: e.DappTitle}
	}
	return item
}

func mpFinclipToProto(e *chatdb.MiniProgramEntry) *chat.MiniProgramFinclip {
	return &chat.MiniProgramFinclip{
		AppId:    e.FinclipAppID,
		Path:     e.FinclipPath,
		Query:    e.FinclipQuery,
		Sequence: e.FinclipSeq,
	}
}

func mpSnapshot(e *chatdb.MiniProgramEntry) *chat.MiniProgramEntrySnapshot {
	return &chat.MiniProgramEntrySnapshot{
		Name:      e.Name,
		IconUrl:   e.IconURL,
		EntryType: e.EntryType,
	}
}

func mpPlatformAllowed(platforms []string, platform string) bool {
	if len(platforms) == 0 || platform == "" {
		return true
	}
	for _, p := range platforms {
		if p == platform {
			return true
		}
	}
	return false
}

func mpEffectiveScope(requested, allowed []string) []string {
	if len(allowed) == 0 {
		allowed = []string{mpDefaultScopeRead}
	}
	if len(requested) == 0 {
		return allowed
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, s := range allowed {
		allowedSet[s] = struct{}{}
	}
	var out []string
	seen := make(map[string]struct{})
	for _, s := range requested {
		if _, ok := allowedSet[s]; !ok {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return allowed
	}
	return out
}

func mpDedup(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func mpFormatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func mpHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func mpRandomToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", errs.WrapMsg(err, "generate ticket token")
	}
	return hex.EncodeToString(buf), nil
}

// compareSemver returns -1, 0 or 1 comparing dot-separated numeric versions.
// Non-numeric or missing parts are treated as 0; it is intentionally lenient so a
// malformed client version never bypasses the minimum-version gate silently.
func compareSemver(a, b string) int {
	pa := strings.Split(a, ".")
	pb := strings.Split(b, ".")
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(strings.TrimSpace(pa[i]))
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(strings.TrimSpace(pb[i]))
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}
