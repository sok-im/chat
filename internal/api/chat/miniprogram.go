package chat

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/openimsdk/chat/pkg/common/mctx"
	"github.com/openimsdk/chat/pkg/eerrs"
	chatpb "github.com/openimsdk/chat/pkg/protocol/chat"
	"github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/tools/errs"
)

const (
	mpRoutePrefixApp      = "/sok/app/appIm/miniProgram"
	mpRoutePrefixInternal = "/sok/internal/miniProgram"

	mpHeaderRequestID     = "X-Request-Id"
	mpHeaderIdempotency   = "Idempotency-Key"
	mpHeaderClientVersion = "X-Client-Version"
	mpHeaderPlatform      = "X-Platform"
	mpHeaderServiceToken  = "X-Service-Token"
	mpHeaderIfNoneMatch   = "If-None-Match"
)

// mpEnvelope is the unified MiniProgram API response envelope. Success uses
// {code:"OK", data, requestId}; failure uses {code, message, requestId}.
type mpEnvelope struct {
	Code      string `json:"code"`
	Data      any    `json:"data,omitempty"`
	Message   string `json:"message,omitempty"`
	RequestID string `json:"requestId"`
}

// mpErrHTTP maps the stable business code to its HTTP status.
var mpErrHTTP = map[string]int{
	"MP_VALIDATION_FAILED":  http.StatusBadRequest,
	"MP_UNAUTHENTICATED":    http.StatusUnauthorized,
	"MP_FORBIDDEN":          http.StatusForbidden,
	"MP_ENTRY_NOT_FOUND":    http.StatusNotFound,
	"MP_ENTRY_SUSPENDED":    http.StatusConflict,
	"MP_VERSION_TOO_LOW":    http.StatusUpgradeRequired,
	"MP_FAVORITES_CONFLICT": http.StatusConflict,
	"MP_RATE_LIMITED":       http.StatusTooManyRequests,
	"MP_INTERNAL":           http.StatusInternalServerError,
}

// mpErrMessage maps the business code to a user-facing message. The message is
// only for display/logging; clients branch on `code`, never on `message`.
var mpErrMessage = map[string]string{
	"MP_VALIDATION_FAILED":  "请求参数有误",
	"MP_UNAUTHENTICATED":    "请先登录",
	"MP_FORBIDDEN":          "无权限或不在灰度范围",
	"MP_ENTRY_NOT_FOUND":    "条目不存在",
	"MP_ENTRY_SUSPENDED":    "该小程序暂不可用",
	"MP_VERSION_TOO_LOW":    "请升级到最新版本",
	"MP_FAVORITES_CONFLICT": "收藏已在其他设备更新，请刷新后重试",
	"MP_RATE_LIMITED":       "请求过于频繁，请稍后再试",
	"MP_INTERNAL":           "服务暂时不可用，请稍后再试",
}

// miniProgramDefaultOperationID injects a default OperationID for MiniProgram
// routes so SOK App clients that do not send OpenIM's operationID header pass the
// global GinParseOperationID middleware. It reuses the request id when present.
func miniProgramDefaultOperationID() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, mpRoutePrefixApp) || strings.HasPrefix(path, mpRoutePrefixInternal) {
			if c.Request.Header.Get(constant.OperationID) == "" {
				opID := c.Request.Header.Get(mpHeaderRequestID)
				if opID == "" {
					opID = strconv.FormatInt(time.Now().UnixNano(), 10)
				}
				c.Request.Header.Set(constant.OperationID, opID)
			}
		}
		c.Next()
	}
}

func mpRequestID(c *gin.Context) string {
	rid := c.GetHeader(mpHeaderRequestID)
	if rid == "" {
		rid = uuid.New().String()
	}
	c.Header(mpHeaderRequestID, rid)
	return rid
}

func mpSuccess(c *gin.Context, requestID string, data any) {
	c.JSON(http.StatusOK, mpEnvelope{Code: "OK", Data: data, RequestID: requestID})
}

// mpErrorWithData maps err to the envelope, optionally attaching data (used for
// the favorites conflict which must return the current set).
func mpErrorWithData(c *gin.Context, requestID string, err error, data any) {
	code := "MP_INTERNAL"
	var codeErr errs.CodeError
	if errors.As(err, &codeErr) {
		if msg := codeErr.Msg(); msg != "" {
			if _, ok := mpErrHTTP[msg]; ok {
				code = msg
			} else if errors.Is(err, errs.ErrNoPermission) {
				code = "MP_UNAUTHENTICATED"
			}
		}
	}
	if errors.Is(err, errs.ErrNoPermission) {
		code = "MP_UNAUTHENTICATED"
	}
	status := mpErrHTTP[code]
	if status == 0 {
		status = http.StatusInternalServerError
	}
	c.JSON(status, mpEnvelope{Code: code, Message: mpErrMessage[code], Data: data, RequestID: requestID})
}

func mpError(c *gin.Context, requestID string, err error) {
	mpErrorWithData(c, requestID, err, nil)
}

// ===================== internal service auth =====================

func (o *Api) checkMiniProgramServiceToken(c *gin.Context) {
	token := c.GetHeader(mpHeaderServiceToken)
	authorized := false
	if token != "" {
		for _, t := range o.miniProgramServiceTokens {
			if t != "" && t == token {
				authorized = true
				break
			}
		}
	}
	if !authorized {
		requestID := mpRequestID(c)
		c.AbortWithStatusJSON(http.StatusForbidden, mpEnvelope{
			Code:      "MP_FORBIDDEN",
			Message:   mpErrMessage["MP_FORBIDDEN"],
			RequestID: requestID,
		})
		return
	}
	c.Next()
}

// ===================== DTOs =====================

type mpFinclipDTO struct {
	AppID    string `json:"appId"`
	Path     string `json:"path"`
	Query    string `json:"query"`
	Sequence int32  `json:"sequence"`
}

type mpDappDTO struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}

type mpCategoryDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Sort int32  `json:"sort"`
}

type mpEntryDTO struct {
	ID            string        `json:"id"`
	EntryType     string        `json:"entryType"`
	Name          string        `json:"name"`
	Description   string        `json:"description"`
	IconURL       string        `json:"iconUrl"`
	CategoryIDs   []string      `json:"categoryIds"`
	Finclip       *mpFinclipDTO `json:"finclip"`
	Dapp          *mpDappDTO    `json:"dapp"`
	Permissions   []string      `json:"permissions"`
	MinAppVersion string        `json:"minAppVersion"`
	Revision      int64         `json:"revision"`
	Sort          int32         `json:"sort"`
}

type mpSnapshotDTO struct {
	Name      string `json:"name"`
	IconURL   string `json:"iconUrl"`
	EntryType string `json:"entryType"`
}

func mpFinclipDTOOf(p *chatpb.MiniProgramFinclip) *mpFinclipDTO {
	if p == nil {
		return nil
	}
	return &mpFinclipDTO{AppID: p.AppId, Path: p.Path, Query: p.Query, Sequence: p.Sequence}
}

func mpDappDTOOf(p *chatpb.MiniProgramDapp) *mpDappDTO {
	if p == nil {
		return nil
	}
	return &mpDappDTO{URL: p.Url, Title: p.Title}
}

func mpEntryDTOOf(p *chatpb.MiniProgramEntry) mpEntryDTO {
	return mpEntryDTO{
		ID:            p.Id,
		EntryType:     p.EntryType,
		Name:          p.Name,
		Description:   p.Description,
		IconURL:       p.IconUrl,
		CategoryIDs:   mpStrings(p.CategoryIds),
		Finclip:       mpFinclipDTOOf(p.Finclip),
		Dapp:          mpDappDTOOf(p.Dapp),
		Permissions:   mpStrings(p.Permissions),
		MinAppVersion: p.MinAppVersion,
		Revision:      p.Revision,
		Sort:          p.Sort,
	}
}

func mpSnapshotDTOOf(p *chatpb.MiniProgramEntrySnapshot) *mpSnapshotDTO {
	if p == nil {
		return nil
	}
	return &mpSnapshotDTO{Name: p.Name, IconURL: p.IconUrl, EntryType: p.EntryType}
}

func mpStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func mpAtoi32(s string) int32 {
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return int32(n)
}

// ===================== App API handlers =====================

func (o *Api) MiniProgramCatalog(c *gin.Context) {
	requestID := mpRequestID(c)
	resp, err := o.chatClient.MiniProgramCatalog(c, &chatpb.MiniProgramCatalogReq{
		UserID:        mctx.GetOpUserID(c),
		CategoryId:    c.Query("categoryId"),
		Keyword:       c.Query("keyword"),
		Cursor:        c.Query("cursor"),
		Limit:         mpAtoi32(c.Query("limit")),
		Source:        c.Query("source"),
		ClientVersion: c.GetHeader(mpHeaderClientVersion),
		Platform:      c.GetHeader(mpHeaderPlatform),
	})
	if err != nil {
		mpError(c, requestID, err)
		return
	}
	if resp.Etag != "" {
		c.Header("ETag", resp.Etag)
		c.Header("Cache-Control", "private, max-age=600")
		c.Header("Vary", "token")
		if match := c.GetHeader(mpHeaderIfNoneMatch); match != "" && match == resp.Etag {
			c.Status(http.StatusNotModified)
			return
		}
	}
	categories := make([]mpCategoryDTO, 0, len(resp.Categories))
	for _, cat := range resp.Categories {
		categories = append(categories, mpCategoryDTO{ID: cat.Id, Name: cat.Name, Sort: cat.Sort})
	}
	items := make([]mpEntryDTO, 0, len(resp.Items))
	for _, item := range resp.Items {
		items = append(items, mpEntryDTOOf(item))
	}
	var nextCursor *string
	if resp.NextCursor != "" {
		nextCursor = &resp.NextCursor
	}
	mpSuccess(c, requestID, gin.H{
		"revision":   resp.Revision,
		"categories": categories,
		"items":      items,
		"nextCursor": nextCursor,
	})
}

func (o *Api) MiniProgramLaunch(c *gin.Context) {
	requestID := mpRequestID(c)
	var body struct {
		Source          string `json:"source"`
		CatalogRevision int64  `json:"catalogRevision"`
	}
	_ = c.ShouldBindJSON(&body)
	resp, err := o.chatClient.MiniProgramLaunch(c, &chatpb.MiniProgramLaunchReq{
		UserID:          mctx.GetOpUserID(c),
		EntryId:         c.Param("entryId"),
		Source:          body.Source,
		CatalogRevision: body.CatalogRevision,
		ClientVersion:   c.GetHeader(mpHeaderClientVersion),
		Platform:        c.GetHeader(mpHeaderPlatform),
	})
	if err != nil {
		mpError(c, requestID, err)
		return
	}
	mpSuccess(c, requestID, gin.H{
		"entryId":      resp.EntryId,
		"revision":     resp.Revision,
		"entryType":    resp.EntryType,
		"finclip":      mpFinclipDTOOf(resp.Finclip),
		"dapp":         mpDappDTOOf(resp.Dapp),
		"expiresAt":    resp.ExpiresAt,
		"staleCatalog": resp.StaleCatalog,
	})
}

func (o *Api) MiniProgramSessionIssue(c *gin.Context) {
	requestID := mpRequestID(c)
	var body struct {
		EntryID        string   `json:"entryId"`
		FinclipAppID   string   `json:"finclipAppId"`
		InstallID      string   `json:"installId"`
		RequestedScope []string `json:"requestedScope"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		mpError(c, requestID, eerrs.ErrMpValidationFailed.WrapMsg("invalid body"))
		return
	}
	resp, err := o.chatClient.MiniProgramSessionIssue(c, &chatpb.MiniProgramSessionIssueReq{
		UserID:         mctx.GetOpUserID(c),
		EntryId:        body.EntryID,
		FinclipAppId:   body.FinclipAppID,
		InstallId:      body.InstallID,
		RequestedScope: body.RequestedScope,
		IdempotencyKey: c.GetHeader(mpHeaderIdempotency),
	})
	if err != nil {
		mpError(c, requestID, err)
		return
	}
	mpSuccess(c, requestID, gin.H{
		"ticket":       resp.Ticket,
		"tokenType":    resp.TokenType,
		"expiresIn":    resp.ExpiresIn,
		"scope":        mpStrings(resp.Scope),
		"refreshAfter": resp.RefreshAfter,
	})
}

func (o *Api) MiniProgramGetRecent(c *gin.Context) {
	requestID := mpRequestID(c)
	resp, err := o.chatClient.MiniProgramGetRecent(c, &chatpb.MiniProgramGetRecentReq{
		UserID: mctx.GetOpUserID(c),
		Limit:  mpAtoi32(c.Query("limit")),
	})
	if err != nil {
		mpError(c, requestID, err)
		return
	}
	items := make([]gin.H, 0, len(resp.Items))
	for _, item := range resp.Items {
		items = append(items, gin.H{
			"entryId":       item.EntryId,
			"lastOpenedAt":  item.LastOpenedAt,
			"entrySnapshot": mpSnapshotDTOOf(item.EntrySnapshot),
		})
	}
	mpSuccess(c, requestID, gin.H{"items": items})
}

func (o *Api) MiniProgramPutRecent(c *gin.Context) {
	requestID := mpRequestID(c)
	var body struct {
		OpenedAt        string `json:"openedAt"`
		CatalogRevision int64  `json:"catalogRevision"`
	}
	_ = c.ShouldBindJSON(&body)
	resp, err := o.chatClient.MiniProgramPutRecent(c, &chatpb.MiniProgramPutRecentReq{
		UserID:          mctx.GetOpUserID(c),
		EntryId:         c.Param("entryId"),
		OpenedAt:        body.OpenedAt,
		CatalogRevision: body.CatalogRevision,
	})
	if err != nil {
		mpError(c, requestID, err)
		return
	}
	mpSuccess(c, requestID, gin.H{
		"entryId":      resp.EntryId,
		"lastOpenedAt": resp.LastOpenedAt,
		"total":        resp.Total,
	})
}

func (o *Api) MiniProgramGetFavorites(c *gin.Context) {
	requestID := mpRequestID(c)
	resp, err := o.chatClient.MiniProgramGetFavorites(c, &chatpb.MiniProgramGetFavoritesReq{
		UserID: mctx.GetOpUserID(c),
	})
	if err != nil {
		mpError(c, requestID, err)
		return
	}
	items := make([]gin.H, 0, len(resp.Items))
	for _, item := range resp.Items {
		items = append(items, gin.H{
			"entryId":       item.EntryId,
			"available":     item.Available,
			"entrySnapshot": mpSnapshotDTOOf(item.EntrySnapshot),
		})
	}
	mpSuccess(c, requestID, gin.H{
		"revision": resp.Revision,
		"entryIds": mpStrings(resp.EntryIds),
		"items":    items,
	})
}

func (o *Api) MiniProgramPutFavorites(c *gin.Context) {
	requestID := mpRequestID(c)
	var body struct {
		EntryIDs []string `json:"entryIds"`
		Revision int64    `json:"revision"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		mpError(c, requestID, eerrs.ErrMpValidationFailed.WrapMsg("invalid body"))
		return
	}
	resp, err := o.chatClient.MiniProgramPutFavorites(c, &chatpb.MiniProgramPutFavoritesReq{
		UserID:   mctx.GetOpUserID(c),
		EntryIds: body.EntryIDs,
		Revision: body.Revision,
	})
	if err != nil {
		mpError(c, requestID, err)
		return
	}
	if resp.Conflict {
		mpErrorWithData(c, requestID, eerrs.ErrMpFavoritesConflict, gin.H{
			"revision": resp.Revision,
			"entryIds": mpStrings(resp.EntryIds),
		})
		return
	}
	mpSuccess(c, requestID, gin.H{
		"revision": resp.Revision,
		"entryIds": mpStrings(resp.EntryIds),
	})
}

func (o *Api) MiniProgramRuntimeConfig(c *gin.Context) {
	requestID := mpRequestID(c)
	resp, err := o.chatClient.MiniProgramRuntimeConfig(c, &chatpb.MiniProgramRuntimeConfigReq{
		UserID:        mctx.GetOpUserID(c),
		ClientVersion: c.GetHeader(mpHeaderClientVersion),
	})
	if err != nil {
		mpError(c, requestID, err)
		return
	}
	mpSuccess(c, requestID, gin.H{
		"enabled":          resp.Enabled,
		"catalogEnabled":   resp.CatalogEnabled,
		"allowOfflineOpen": resp.AllowOfflineOpen,
		"minClientVersion": resp.MinClientVersion,
		"cacheTtlSeconds":  resp.CacheTtlSeconds,
	})
}

func (o *Api) MiniProgramReportEvents(c *gin.Context) {
	requestID := mpRequestID(c)
	var body struct {
		Events []struct {
			Type         string `json:"type"`
			EntryID      string `json:"entryId"`
			FinclipAppID string `json:"finclipAppId"`
			Platform     string `json:"platform"`
			SDKVersion   string `json:"sdkVersion"`
			DurationMs   int64  `json:"durationMs"`
			ErrorCode    string `json:"errorCode"`
			OccurredAt   string `json:"occurredAt"`
		} `json:"events"`
	}
	_ = c.ShouldBindJSON(&body)
	events := make([]*chatpb.MiniProgramEvent, 0, len(body.Events))
	for _, e := range body.Events {
		events = append(events, &chatpb.MiniProgramEvent{
			Type:         e.Type,
			EntryId:      e.EntryID,
			FinclipAppId: e.FinclipAppID,
			Platform:     e.Platform,
			SdkVersion:   e.SDKVersion,
			DurationMs:   e.DurationMs,
			ErrorCode:    e.ErrorCode,
			OccurredAt:   e.OccurredAt,
		})
	}
	// Telemetry ingestion must never block; always report success.
	_, _ = o.chatClient.MiniProgramReportEvents(c, &chatpb.MiniProgramReportEventsReq{
		UserID: mctx.GetOpUserID(c),
		Events: events,
	})
	mpSuccess(c, requestID, gin.H{})
}

// ===================== Internal API handlers =====================

func (o *Api) MiniProgramSessionIntrospect(c *gin.Context) {
	requestID := mpRequestID(c)
	var body struct {
		Token         string `json:"token"`
		ExpectedAppID string `json:"expectedAppId"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		mpError(c, requestID, eerrs.ErrMpValidationFailed.WrapMsg("invalid body"))
		return
	}
	resp, err := o.chatClient.MiniProgramSessionIntrospect(c, &chatpb.MiniProgramSessionIntrospectReq{
		Token:         body.Token,
		ExpectedAppId: body.ExpectedAppID,
	})
	if err != nil {
		mpError(c, requestID, err)
		return
	}
	if !resp.Active {
		mpSuccess(c, requestID, gin.H{"active": false})
		return
	}
	mpSuccess(c, requestID, gin.H{
		"active":       true,
		"subject":      resp.Subject,
		"finclipAppId": resp.FinclipAppId,
		"entryId":      resp.EntryId,
		"scope":        mpStrings(resp.Scope),
		"expiresAt":    resp.ExpiresAt,
	})
}

func (o *Api) MiniProgramSessionRevoke(c *gin.Context) {
	requestID := mpRequestID(c)
	var body struct {
		UserID       string `json:"userId"`
		InstallID    string `json:"installId"`
		EntryID      string `json:"entryId"`
		FinclipAppID string `json:"finclipAppId"`
		JTI          string `json:"jti"`
		Reason       string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		mpError(c, requestID, eerrs.ErrMpValidationFailed.WrapMsg("invalid body"))
		return
	}
	resp, err := o.chatClient.MiniProgramSessionRevoke(c, &chatpb.MiniProgramSessionRevokeReq{
		UserID:       body.UserID,
		InstallId:    body.InstallID,
		EntryId:      body.EntryID,
		FinclipAppId: body.FinclipAppID,
		Jti:          body.JTI,
		Reason:       body.Reason,
	})
	if err != nil {
		mpError(c, requestID, err)
		return
	}
	mpSuccess(c, requestID, gin.H{
		"revokedCount": resp.RevokedCount,
		"notBefore":    resp.NotBefore,
	})
}
