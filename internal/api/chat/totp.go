package chat

import (
	"github.com/gin-gonic/gin"
	"github.com/openimsdk/chat/pkg/common/apistruct"
	"github.com/openimsdk/chat/pkg/common/mctx"
	chatpb "github.com/openimsdk/chat/pkg/protocol/chat"
	"github.com/openimsdk/tools/a2r"
	"github.com/openimsdk/tools/apiresp"
	"github.com/openimsdk/tools/errs"
)

// TotpGetSecret generates a temporary TOTP binding secret for the authenticated user.
// POST /totp/secret
func (o *Api) TotpGetSecret(c *gin.Context) {
	userID := mctx.GetOpUserID(c)
	if userID == "" {
		apiresp.GinError(c, errs.ErrNoPermission.WrapMsg("missing user id"))
		return
	}
	resp, err := o.chatClient.GetTotpSecret(c, &chatpb.GetTotpSecretReq{UserID: userID})
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, resp)
}

// TotpBind confirms the TOTP binding by verifying the first code.
// POST /totp/bind
func (o *Api) TotpBind(c *gin.Context) {
	userID := mctx.GetOpUserID(c)
	if userID == "" {
		apiresp.GinError(c, errs.ErrNoPermission.WrapMsg("missing user id"))
		return
	}
	req, err := a2r.ParseRequest[struct {
		TotpCode string `json:"totpCode" binding:"required"`
	}](c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	resp, err := o.chatClient.BindTotp(c, &chatpb.BindTotpReq{
		UserID:   userID,
		TotpCode: req.TotpCode,
	})
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, resp)
}

// TotpVerify is the second login step: validate TOTP/recovery code then return full credentials.
// POST /totp/verify  (no auth token required)
func (o *Api) TotpVerify(c *gin.Context) {
	req, err := a2r.ParseRequest[struct {
		MfaToken string `json:"mfaToken" binding:"required"`
		TotpCode string `json:"totpCode" binding:"required"`
		Platform int32  `json:"platform"`
	}](c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}

	resp, err := o.chatClient.VerifyTotp(c, &chatpb.VerifyTotpReq{
		MfaToken: req.MfaToken,
		TotpCode: req.TotpCode,
		Platform: req.Platform,
	})
	if err != nil {
		apiresp.GinError(c, err)
		return
	}

	adminToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiCtx := mctx.WithApiToken(c, adminToken)
	imToken, err := o.imApiCaller.GetUserToken(apiCtx, resp.UserID, req.Platform)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}

	apiresp.GinSuccess(c, &apistruct.LoginResp{
		ImToken:   imToken,
		ChatToken: resp.ChatToken,
		UserID:    resp.UserID,
	})
}

// TotpGetStatus returns whether the authenticated user has TOTP bound.
// POST /totp/status
func (o *Api) TotpGetStatus(c *gin.Context) {
	userID := mctx.GetOpUserID(c)
	if userID == "" {
		apiresp.GinError(c, errs.ErrNoPermission.WrapMsg("missing user id"))
		return
	}
	resp, err := o.chatClient.GetTotpStatus(c, &chatpb.GetTotpStatusReq{UserID: userID})
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, resp)
}

// TotpUnbind removes the TOTP binding after verifying the current code.
// POST /totp/unbind
func (o *Api) TotpUnbind(c *gin.Context) {
	userID := mctx.GetOpUserID(c)
	if userID == "" {
		apiresp.GinError(c, errs.ErrNoPermission.WrapMsg("missing user id"))
		return
	}
	req, err := a2r.ParseRequest[struct {
		TotpCode string `json:"totpCode" binding:"required"`
	}](c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	resp, err := o.chatClient.UnbindTotp(c, &chatpb.UnbindTotpReq{
		UserID:   userID,
		TotpCode: req.TotpCode,
	})
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, resp)
}
