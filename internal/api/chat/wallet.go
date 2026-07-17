package chat

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openimsdk/chat/pkg/common/mctx"
	"github.com/openimsdk/chat/pkg/common/wallet"
	"github.com/openimsdk/chat/pkg/protocol/admin"
	chatpb "github.com/openimsdk/chat/pkg/protocol/chat"
	"github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/protocol/sdkws"
	"github.com/openimsdk/tools/errs"
)

const walletRoutePrefix = "/sok/app/appWallet"

// walletDefaultOperationID injects a default operationID for wallet routes so that
// clients ported from the Java sok-api (which never send OpenIM's operationID header)
// are not rejected by the global GinParseOperationID middleware. Per the source
// contract the value is the current Unix seconds timestamp.
func walletDefaultOperationID() gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, walletRoutePrefix) {
			if c.Request.Header.Get(constant.OperationID) == "" {
				c.Request.Header.Set(constant.OperationID, strconv.FormatInt(time.Now().Unix(), 10))
			}
		}
		c.Next()
	}
}

type walletR struct {
	Code int    `json:"code"`
	Data any    `json:"data"`
	Msg  string `json:"msg"`
}

func walletSuccess(c *gin.Context, data any) {
	c.JSON(http.StatusOK, walletR{Code: 200, Data: data, Msg: "success"})
}

func walletFail(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, walletR{Code: 500, Data: nil, Msg: msg})
}

func walletFailFromErr(c *gin.Context, err error) {
	if err == nil {
		walletFail(c, string(wallet.ErrCommonFail))
		return
	}
	// Wallet business errors and downstream login/register errors both cross the
	// gRPC boundary as errs.CodeError; Msg() carries the original key/message
	// (Java R.fail(msg)). Avoid Error() which prepends the numeric code + detail.
	var codeErr errs.CodeError
	if errors.As(err, &codeErr) && codeErr.Msg() != "" {
		walletFail(c, codeErr.Msg())
		return
	}
	walletFail(c, err.Error())
}

type getSignKeyRet struct {
	Str     string `json:"str"`
	TraceID string `json:"traceId"`
}

type checkWalletAddressParams struct {
	TraceID string             `json:"traceId"`
	Evm     *walletChainParams `json:"evm"`
	Tron    *walletChainParams `json:"tron"`
	Bitcoin *walletChainParams `json:"bitcoin"`
	Solana  *walletChainParams `json:"solana"`
}

type walletChainParams struct {
	Address string `json:"address"`
	Sign    string `json:"sign"`
	MsgHash string `json:"msgHash"`
}

type checkWalletAddressRet struct {
	IsRegister int `json:"isRegister"`
}

type appLoginParams struct {
	DeviceID       string             `json:"deviceID"`
	Platform       int32              `json:"platform"`
	InvitationCode string             `json:"invitationCode"`
	FirstName      string             `json:"firstName"`
	LastName       string             `json:"lastName"`
	Language       string             `json:"language"`
	Gender         int32              `json:"gender"`
	TraceID        string             `json:"traceId"`
	Evm            *walletChainParams `json:"evm"`
	Tron           *walletChainParams `json:"tron"`
	Bitcoin        *walletChainParams `json:"bitcoin"`
	Solana         *walletChainParams `json:"solana"`
}

type appLoginRet struct {
	ImToken   string `json:"imToken"`
	ChatToken string `json:"chatToken"`
	UserID    string `json:"userID"`
}

func (o *Api) GetWalletSignKey(c *gin.Context) {
	resp, err := o.chatClient.GetWalletSignKey(c, &chatpb.GetWalletSignKeyReq{})
	if err != nil {
		walletFailFromErr(c, err)
		return
	}
	walletSuccess(c, getSignKeyRet{Str: resp.Str, TraceID: resp.TraceId})
}

func (o *Api) CheckWalletAddress(c *gin.Context) {
	var params checkWalletAddressParams
	if err := c.ShouldBindJSON(&params); err != nil {
		walletFail(c, string(wallet.ErrLeastTransmit))
		return
	}
	if params.TraceID == "" {
		walletFail(c, string(wallet.ErrSignKey))
		return
	}
	resp, err := o.chatClient.CheckWalletAddress(c, &chatpb.CheckWalletAddressReq{
		TraceId: params.TraceID,
		Evm:     toProtoChain(params.Evm),
		Tron:    toProtoChain(params.Tron),
		Bitcoin: toProtoChain(params.Bitcoin),
		Solana:  toProtoChain(params.Solana),
	})
	if err != nil {
		walletFailFromErr(c, err)
		return
	}
	walletSuccess(c, checkWalletAddressRet{IsRegister: int(resp.IsRegister)})
}

func (o *Api) WalletAppLogin(c *gin.Context) {
	var params appLoginParams
	if err := c.ShouldBindJSON(&params); err != nil {
		walletFail(c, string(wallet.ErrLeastTransmit))
		return
	}
	if params.TraceID == "" || params.DeviceID == "" || params.Platform < 1 {
		walletFail(c, string(wallet.ErrLeastTransmit))
		return
	}
	ip, err := o.GetClientIP(c)
	if err != nil {
		walletFail(c, string(wallet.ErrCommonFail))
		return
	}
	resp, err := o.chatClient.WalletAppLogin(c, &chatpb.WalletAppLoginReq{
		DeviceID:       params.DeviceID,
		Platform:       params.Platform,
		InvitationCode: params.InvitationCode,
		FirstName:      params.FirstName,
		LastName:       params.LastName,
		Language:       params.Language,
		Gender:         params.Gender,
		TraceId:        params.TraceID,
		Evm:            toProtoChain(params.Evm),
		Tron:           toProtoChain(params.Tron),
		Bitcoin:        toProtoChain(params.Bitcoin),
		Solana:         toProtoChain(params.Solana),
		Ip:             ip,
	})
	if err != nil {
		walletFailFromErr(c, err)
		return
	}
	if resp.ChatToken == "" || resp.UserID == "" {
		walletFail(c, string(wallet.ErrCommonFail))
		return
	}

	imToken, err := o.walletFetchIMToken(c, resp.UserID, params.Platform, resp.NewUser, resp.UserInfo)
	if err != nil {
		walletFailFromErr(c, err)
		return
	}
	walletSuccess(c, appLoginRet{
		ImToken:   imToken,
		ChatToken: resp.ChatToken,
		UserID:    resp.UserID,
	})
}

func (o *Api) walletFetchIMToken(c *gin.Context, userID string, platform int32, newUser bool, userInfo *chatpb.RegisterUserInfo) (string, error) {
	adminToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
	if err != nil {
		return "", err
	}
	apiCtx := mctx.WithApiToken(c, adminToken)
	if newUser && userInfo != nil {
		user := &sdkws.UserInfo{
			UserID:     userID,
			Nickname:   userInfo.Nickname,
			FaceURL:    o.defaultFaceURL,
			CreateTime: time.Now().UnixMilli(),
			FirstName:  userInfo.FirstName,
			LastName:   userInfo.LastName,
			Language:   userInfo.Language,
		}
		if err := o.imApiCaller.RegisterUser(apiCtx, []*sdkws.UserInfo{user}); err != nil {
			return "", err
		}
		rpcCtx := o.WithAdminUser(c)
		if resp, err := o.adminClient.FindDefaultFriend(rpcCtx, &admin.FindDefaultFriendReq{}); err == nil {
			_ = o.imApiCaller.ImportFriend(apiCtx, userID, resp.UserIDs)
		}
		if resp, err := o.adminClient.FindDefaultGroup(rpcCtx, &admin.FindDefaultGroupReq{}); err == nil {
			_ = o.imApiCaller.InviteToGroup(apiCtx, userID, resp.GroupIDs)
		}
	}
	return o.imApiCaller.GetUserToken(apiCtx, userID, platform)
}

func toProtoChain(p *walletChainParams) *chatpb.WalletChainParams {
	if p == nil {
		return nil
	}
	return &chatpb.WalletChainParams{
		Address: p.Address,
		Sign:    p.Sign,
		MsgHash: p.MsgHash,
	}
}
