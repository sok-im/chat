// Copyright © 2023 OpenIM open source community. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package chat

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/openimsdk/chat/internal/api/util"

	"github.com/gin-gonic/gin"
	"github.com/openimsdk/chat/pkg/common/apistruct"
	"github.com/openimsdk/chat/pkg/common/constant"
	"github.com/openimsdk/chat/pkg/common/imapi"
	"github.com/openimsdk/chat/pkg/common/mctx"
	"github.com/openimsdk/chat/pkg/protocol/admin"
	chatpb "github.com/openimsdk/chat/pkg/protocol/chat"
	constantpb "github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/protocol/sdkws"
	"github.com/openimsdk/tools/a2r"
	"github.com/openimsdk/tools/apiresp"
	"github.com/openimsdk/tools/errs"
	"github.com/openimsdk/tools/log"
)

func New(chatClient chatpb.ChatClient, adminClient admin.AdminClient, imApiCaller imapi.CallerInterface, api *util.Api, defaultFaceURL string) *Api {
	return &Api{
		Api:            api,
		chatClient:     chatClient,
		adminClient:    adminClient,
		imApiCaller:    imApiCaller,
		defaultFaceURL: defaultFaceURL,
	}
}

type Api struct {
	*util.Api
	chatClient     chatpb.ChatClient
	adminClient    admin.AdminClient
	imApiCaller    imapi.CallerInterface
	defaultFaceURL string
}

// ################## ACCOUNT ##################

func (o *Api) SendVerifyCode(c *gin.Context) {
	req, err := a2r.ParseRequest[chatpb.SendVerifyCodeReq](c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	ip, err := o.GetClientIP(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	req.Ip = ip
	resp, err := o.chatClient.SendVerifyCode(c, req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, resp)
}

func (o *Api) VerifyCode(c *gin.Context) {
	req, err := a2r.ParseRequest[chatpb.VerifyCodeReq](c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	resp, err := o.chatClient.VerifyCode(c, req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	if !resp.Verified {
		c.JSON(http.StatusOK, &apiresp.ApiResponse{
			ErrCode: int(resp.ErrCode),
			ErrMsg:  resp.ErrMsg,
			Data:    resp,
		})
		return
	}
	apiresp.GinSuccess(c, resp)
}

func (o *Api) RegisterUser(c *gin.Context) {
	req, err := a2r.ParseRequest[chatpb.RegisterUserReq](c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	ip, err := o.GetClientIP(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	req.Ip = ip

	imToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiCtx := mctx.WithApiToken(c, imToken)
	rpcCtx := o.WithAdminUser(c)

	baseNickname := req.User.Nickname
	if baseNickname == "" {
		baseNickname = strings.Split(uuid.New().String(), "-")[0]
	}
	nickname, err := o.generateUniqueNickname(rpcCtx, baseNickname)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	req.User.Nickname = nickname

	if req.User.FaceURL == "" {
		req.User.FaceURL = o.defaultFaceURL
	}
	if req.User.FirstName == "" && req.User.LastName == "" {
		req.User.FirstName = "SokIM"
		n, randErr := rand.Int(rand.Reader, big.NewInt(26))
		if randErr != nil {
			req.User.LastName = "UserA"
		} else {
			req.User.LastName = "User" + string(rune('A'+n.Int64()))
		}
	}

	if req.User.Language == "" {
		req.User.Language = "en"
	}

	respRegisterUser, err := o.chatClient.RegisterUser(c, req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}

	userInfo := &sdkws.UserInfo{
		UserID:     respRegisterUser.UserID,
		Nickname:   req.User.Nickname,
		FaceURL:    req.User.FaceURL,
		CreateTime: time.Now().UnixMilli(),
		FirstName:  req.User.FirstName,
		LastName:   req.User.LastName,
		Phone:      req.User.PhoneNumber,
		AreaCode:   req.User.AreaCode,
		Language:   req.User.Language,
	}
	err = o.imApiCaller.RegisterUser(apiCtx, []*sdkws.UserInfo{userInfo})
	if err != nil {
		apiresp.GinError(c, err)
		return
	}

	if resp, err := o.adminClient.FindDefaultFriend(rpcCtx, &admin.FindDefaultFriendReq{}); err == nil {
		_ = o.imApiCaller.ImportFriend(apiCtx, respRegisterUser.UserID, resp.UserIDs)
	}
	if resp, err := o.adminClient.FindDefaultGroup(rpcCtx, &admin.FindDefaultGroupReq{}); err == nil {
		_ = o.imApiCaller.InviteToGroup(apiCtx, respRegisterUser.UserID, resp.GroupIDs)
	}
	var resp apistruct.UserRegisterResp
	if req.AutoLogin {
		resp.ImToken, err = o.imApiCaller.GetUserToken(apiCtx, respRegisterUser.UserID, req.Platform)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
	}
	resp.ChatToken = respRegisterUser.ChatToken
	resp.UserID = respRegisterUser.UserID
	apiresp.GinSuccess(c, &resp)
}

func (o *Api) Login(c *gin.Context) {
	req, err := a2r.ParseRequest[chatpb.LoginReq](c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	ip, err := o.GetClientIP(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	req.Ip = ip
	resp, err := o.chatClient.Login(c, req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	if resp.MfaRequired {
		apiresp.GinSuccess(c, &apistruct.LoginResp{
			MfaRequired:      true,
			MfaToken:         resp.MfaToken,
			MfaTokenExpireAt: resp.MfaTokenExpireAt,
		})
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
		UserID:    resp.UserID,
		ChatToken: resp.ChatToken,
	})
}

func (o *Api) UserIdentLogin(c *gin.Context) {
	req, err := a2r.ParseRequest[chatpb.LoginReq](c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	ip, err := o.GetClientIP(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	req.Ip = ip
	resp, err := o.chatClient.Login(c, req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	if resp.MfaRequired {
		apiresp.GinSuccess(c, &apistruct.LoginResp{
			MfaRequired:      true,
			MfaToken:         resp.MfaToken,
			MfaTokenExpireAt: resp.MfaTokenExpireAt,
		})
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
		UserID:    resp.UserID,
		ChatToken: resp.ChatToken,
	})
}

func (o *Api) ResetPassword(c *gin.Context) {
	a2r.Call(c, chatpb.ChatClient.ResetPassword, o.chatClient)
}

func (o *Api) DelUserAccount(c *gin.Context) {
	log.ZDebug(c, "DelUserAccount start")
	req, err := a2r.ParseRequest[chatpb.DelUserAccountReq](c)
	if err != nil {
		log.ZWarn(c, "DelUserAccount parse request failed", err)
		apiresp.GinError(c, err)
		return
	}
	opUserID := mctx.GetOpUserID(c)
	if opUserID == "" {
		log.ZWarn(c, "DelUserAccount no user id", nil, "req", req)
		apiresp.GinError(c, errs.ErrNoPermission.WrapMsg("no user id"))
		return
	}
	userType, err := mctx.GetUserType(c)
	if err != nil {
		log.ZWarn(c, "DelUserAccount get user type failed", err, "req", req)
		apiresp.GinError(c, errs.ErrNoPermission.WrapMsg("missing user type"))
		return
	}
	if userType != constant.AdminUser {
		// 普通用户只能删除自己的账号
		for _, id := range req.UserIDs {
			if id != opUserID {
				log.ZWarn(c, "DelUserAccount can only delete own account", nil, "req", req)
				apiresp.GinError(c, errs.ErrNoPermission.WrapMsg("can only delete own account"))
				return
			}
		}
		req.UserIDs = []string{opUserID}
	}

	resp, err := o.chatClient.DelUserAccount(c, req)
	if err != nil {
		log.ZWarn(c, "DelUserAccount delete user account failed", err, "req", req)
		apiresp.GinError(c, err)
		return
	}

	imToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
	if err != nil {
		log.ZWarn(c, "DelUserAccount get IM admin token failed", err, "req", req)
		apiresp.GinError(c, err)
		return
	}
	apiCtx := mctx.WithApiToken(c, imToken)
	imAdminUserID := o.GetDefaultIMAdminUserID()
	for _, userID := range req.UserIDs {
		if userID == imAdminUserID {
			continue
		}
		//if err := o.imApiCaller.ForceOffLine(apiCtx, userID); err != nil {
		//	log.ZWarn(c, "DelUserAccount force offline failed", err, "userID", userID)
		//}
		if err := o.imApiCaller.DeleteUsers(apiCtx, userID); err != nil {
			log.ZWarn(c, "DelUserAccount delete IM user failed", err, "userID", userID, "req", req)
		} else {
			log.ZDebug(c, "DelUserAccount delete IM user success", "userID", userID, "req", req)
		}
	}
	apiresp.GinSuccess(c, resp)
}

func (o *Api) ChangePassword(c *gin.Context) {
	req, err := a2r.ParseRequest[chatpb.ChangePasswordReq](c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	resp, err := o.chatClient.ChangePassword(c, req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}

	imToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	err = o.imApiCaller.ForceOffLine(mctx.WithApiToken(c, imToken), req.UserID)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, resp)
}

// ################## USER ##################

func (o *Api) UpdateUserInfo(c *gin.Context) {
	req, err := a2r.ParseRequest[chatpb.UpdateUserInfoReq](c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	respUpdate, err := o.chatClient.UpdateUserInfo(c, req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}

	var imToken string
	imToken, err = o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	var (
		nickName string
		faceURL  string
	)
	if req.Nickname != nil {
		nickName = req.Nickname.Value
	} else {
		nickName = respUpdate.NickName
	}
	if req.FaceURL != nil {
		faceURL = req.FaceURL.Value
	} else {
		faceURL = respUpdate.FaceUrl
	}
	err = o.imApiCaller.UpdateUserInfo(mctx.WithApiToken(c, imToken), req.UserID, nickName, faceURL)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, apistruct.UpdateUserInfoResp{})
}

func (o *Api) FindUserPublicInfo(c *gin.Context) {
	a2r.Call(c, chatpb.ChatClient.FindUserPublicInfo, o.chatClient)
}

func (o *Api) GetUserByPhone(c *gin.Context) {
	a2r.Call(c, chatpb.ChatClient.GetUserByPhone, o.chatClient)
}

func (o *Api) CheckAccountByPhone(c *gin.Context) {
	a2r.Call(c, chatpb.ChatClient.CheckAccountByPhone, o.chatClient)
}

func (o *Api) GetUserByNickname(c *gin.Context) {
	a2r.Call(c, chatpb.ChatClient.GetUserByNickname, o.chatClient)
}

func (o *Api) FindUserFullInfo(c *gin.Context) {
	a2r.Call(c, chatpb.ChatClient.FindUserFullInfo, o.chatClient)
}

func (o *Api) SearchUserFullInfo(c *gin.Context) {
	a2r.Call(c, chatpb.ChatClient.SearchUserFullInfo, o.chatClient)
}

func (o *Api) SearchUserPublicInfo(c *gin.Context) {
	a2r.Call(c, chatpb.ChatClient.SearchUserPublicInfo, o.chatClient)
}

func (o *Api) GetTokenForVideoMeeting(c *gin.Context) {
	a2r.Call(c, chatpb.ChatClient.GetTokenForVideoMeeting, o.chatClient)
}

// ################## APPLET ##################

func (o *Api) FindApplet(c *gin.Context) {
	a2r.Call(c, admin.AdminClient.FindApplet, o.adminClient)
}

// ################## CONFIG ##################

func (o *Api) GetClientConfig(c *gin.Context) {
	a2r.Call(c, admin.AdminClient.GetClientConfig, o.adminClient)
}

// ################## CALLBACK ##################

func (o *Api) OpenIMCallback(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	req := &chatpb.OpenIMCallbackReq{
		Command: c.Query(constantpb.CallbackCommand),
		Body:    string(body),
	}
	if _, err := o.chatClient.OpenIMCallback(c, req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, nil)
}

func (o *Api) SearchFriend(c *gin.Context) {
	req, err := a2r.ParseRequest[struct {
		UserID string `json:"userID"`
		chatpb.SearchUserInfoReq
	}](c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	if req.UserID == "" {
		req.UserID = mctx.GetOpUserID(c)
	}
	imToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	userIDs, err := o.imApiCaller.FriendUserIDs(mctx.WithApiToken(c, imToken), req.UserID)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	if len(userIDs) == 0 {
		apiresp.GinSuccess(c, &chatpb.SearchUserInfoResp{})
		return
	}
	req.SearchUserInfoReq.UserIDs = userIDs
	resp, err := o.chatClient.SearchUserInfo(c, &req.SearchUserInfoReq)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, resp)
}

func (o *Api) LatestApplicationVersion(c *gin.Context) {
	a2r.Call(c, admin.AdminClient.LatestApplicationVersion, o.adminClient)
}

func (o *Api) PageApplicationVersion(c *gin.Context) {
	a2r.Call(c, admin.AdminClient.PageApplicationVersion, o.adminClient)
}

func ensureNicknameLeadingNonDigit(s string) string {
	if s == "" {
		return "u"
	}
	r, _ := utf8.DecodeRuneInString(s)
	if unicode.IsDigit(r) {
		return "u" + s
	}
	return s
}

const maxNicknameGenAttempts = 20

func (o *Api) generateUniqueNickname(ctx context.Context, baseNickname string) (string, error) {
	baseNickname = ensureNicknameLeadingNonDigit(baseNickname)
	for i := 0; i < maxNicknameGenAttempts; i++ {
		nickname := baseNickname + "." + randomNicknameSuffix()
		resp, err := o.chatClient.GetUserByNickname(ctx, &chatpb.GetUserByNicknameReq{
			Nickname:   nickname,
			ExactMatch: true,
			Pagination: &sdkws.RequestPagination{PageNumber: 1, ShowNumber: 1},
		})
		if err != nil {
			return "", err
		}
		if resp.Total == 0 {
			return nickname, nil
		}
	}
	return "", errs.ErrInternalServer.WrapMsg("failed to generate unique nickname")
}

func randomNicknameSuffix() string {
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return "0000"
	}
	return fmt.Sprintf("%04d", n.Int64())
}
