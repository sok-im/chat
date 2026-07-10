package chat

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	constantpb "github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/tools/utils/datautil"

	"github.com/openimsdk/tools/errs"
	"github.com/openimsdk/tools/log"
	"github.com/openimsdk/tools/mcontext"

	"github.com/openimsdk/chat/pkg/common/constant"
	"github.com/openimsdk/chat/pkg/common/db/cache"
	"github.com/openimsdk/chat/pkg/common/db/dbutil"
	chatdb "github.com/openimsdk/chat/pkg/common/db/table/chat"
	"github.com/openimsdk/chat/pkg/eerrs"
	"github.com/openimsdk/chat/pkg/protocol/chat"
)

func (o *chatSvr) verifyCodeJoin(areaCode, phoneNumber string) string {
	return areaCode + " " + phoneNumber
}

// normalizeAreaCode ensures E.164-style country code prefix (+86) for DB lookups.
func normalizeAreaCode(areaCode string) (string, error) {
	if areaCode == "" {
		return "", errs.ErrArgs.WrapMsg("area code must be set")
	}
	if !strings.HasPrefix(areaCode, "+") {
		areaCode = "+" + areaCode
	}
	if _, err := strconv.ParseUint(areaCode[1:], 10, 64); err != nil {
		return "", errs.ErrArgs.WrapMsg("area code must be number")
	}
	return areaCode, nil
}

func (o *chatSvr) needSendVerifyCaptcha(ctx context.Context, account string, now time.Time) (bool, error) {
	if o.Code.NeedVerifyCaptchaCount <= 0 {
		return false, nil
	}
	count, err := o.Database.CountVerifyCodeRange(ctx, account, now.Add(-o.Code.UintTime), now)
	if err != nil {
		return false, err
	}
	return int(count) >= o.Code.NeedVerifyCaptchaCount-1, nil
}

func (o *chatSvr) SendVerifyCode(ctx context.Context, req *chat.SendVerifyCodeReq) (*chat.SendVerifyCodeResp, error) {
	switch int(req.UsedFor) {
	case constant.VerificationCodeForRegister:
		if err := o.Admin.CheckRegister(ctx, req.Ip); err != nil {
			return nil, err
		}
		if req.Email != "" {
			return nil, errs.ErrArgs.WrapMsg("email verify code is disabled")
		}
		if req.AreaCode == "" || req.PhoneNumber == "" {
			return nil, errs.ErrArgs.WrapMsg("area code or phone number is empty")
		}
		if !strings.HasPrefix(req.AreaCode, "+") {
			req.AreaCode = "+" + req.AreaCode
		}
		if _, err := strconv.ParseUint(req.AreaCode[1:], 10, 64); err != nil {
			return nil, errs.ErrArgs.WrapMsg("area code must be number")
		}
		if _, err := strconv.ParseUint(req.PhoneNumber, 10, 64); err != nil {
			return nil, errs.ErrArgs.WrapMsg("phone number must be number")
		}
		conf, err := o.Admin.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		if val := conf[constant.NeedInvitationCodeRegisterConfigKey]; datautil.Contain(strings.ToLower(val), "1", "true", "yes") {
			if req.InvitationCode == "" {
				return nil, errs.ErrArgs.WrapMsg("invitation code is empty")
			}
			if err := o.Admin.CheckInvitationCode(ctx, req.InvitationCode); err != nil {

				return nil, err
			}
		}
	case constant.VerificationCodeForLogin, constant.VerificationCodeForResetPassword:
		if req.Email != "" {
			return nil, errs.ErrArgs.WrapMsg("email verify code is disabled")
		}
		areaCode, err := normalizeAreaCode(req.AreaCode)
		if err != nil {
			return nil, err
		}
		req.AreaCode = areaCode
		attrs, err := o.Database.FindAttributeByPhone(ctx, req.AreaCode, req.PhoneNumber)
		if dbutil.IsDBNotFound(err) || len(attrs) == 0 {
			log.ZError(ctx, "send verify code failed", eerrs.ErrAccountNotFound.WrapMsg("phone unregistered"))
			return nil, eerrs.ErrAccountNotFound.WrapMsg("phone unregistered")
		} else if err != nil {
			log.ZError(ctx, "send verify code failed", err)
			return nil, err
		}

	default:
		log.ZError(ctx, "send verify code failed", errs.ErrArgs.WrapMsg("used unknown"))
		return nil, errs.ErrArgs.WrapMsg("used unknown")
	}

	var (
		code     = o.Code.SuperCode
		account  = o.verifyCodeJoin(req.AreaCode, req.PhoneNumber)
		sendCode func() error
	)

	if o.SMS != nil {
		sendCode = func() error {
			return o.SMS.SendCode(ctx, req.AreaCode, req.PhoneNumber, code, req.Language)
		}
		code = o.genVerifyCode()
	}

	now := time.Now()
	if req.CaptchaID == "" {
		needCaptcha, err := o.needSendVerifyCaptcha(ctx, account, now)
		if err != nil {
			log.ZError(ctx, "send verify code failed", err)
			return nil, err
		}
		if needCaptcha {
			return &chat.SendVerifyCodeResp{NeedVerifyCaptcha: true}, nil
		}
	} else {
		ok, err := o.Database.ConsumeCaptcha(ctx, req.CaptchaID)
		if err != nil {
			log.ZError(ctx, "send verify code failed", err)
			return nil, err
		}
		if !ok {
			timeoutErr := errs.ErrArgs.WrapMsg("captcha verify timeout")
			log.ZError(ctx, "send verify code failed", timeoutErr)
			return nil, timeoutErr
		}
	}
	count, err := o.Database.CountVerifyCodeRange(ctx, account, now.Add(-o.Code.UintTime), now)
	if err != nil {
		log.ZError(ctx, "send verify code failed", err)
		return nil, err
	}
	if o.Code.MaxCount < int(count) {
		log.ZError(ctx, "send verify code failed", eerrs.ErrVerifyCodeSendFrequently.Wrap())
		return nil, eerrs.ErrVerifyCodeSendFrequently.Wrap()
	}

	platformName := constantpb.PlatformIDToName(int(req.Platform))
	if platformName == "" {
		platformName = fmt.Sprintf("platform:%d", req.Platform)
	}
	vc := &chatdb.VerifyCode{
		Account:    account,
		Code:       code,
		Platform:   platformName,
		Duration:   uint(o.Code.ValidTime / time.Second),
		Count:      0,
		Used:       false,
		CreateTime: now,
	}
	if err := o.Database.AddVerifyCode(ctx, vc, sendCode); err != nil {
		log.ZError(ctx, "send verify code failed", err)
		return nil, err
	}
	log.ZDebug(ctx, "send code success", "account", account, "code", code, "platform", platformName)
	return &chat.SendVerifyCodeResp{NeedVerifyCaptcha: false}, nil
}

type verifyCodeOutcome struct {
	id                string
	needVerifyCaptcha bool
	err               error
}

func (o *chatSvr) doVerifyCode(ctx context.Context, account string, verifyCode string) verifyCodeOutcome {
	if verifyCode == "" {
		return verifyCodeOutcome{err: errs.ErrArgs.WrapMsg("verify code is empty")}
	}
	if o.SMS == nil && o.Mail == nil {
		if o.Code.SuperCode != verifyCode {
			return verifyCodeOutcome{err: eerrs.ErrVerifyCodeNotMatch.Wrap()}
		}
		return verifyCodeOutcome{}
	}
	last, err := o.Database.TakeLastVerifyCode(ctx, account)
	if err != nil {
		if dbutil.IsDBNotFound(err) {
			return verifyCodeOutcome{err: eerrs.ErrVerifyCodeExpired.Wrap()}
		}
		return verifyCodeOutcome{err: err}
	}
	if last.CreateTime.Unix()+int64(last.Duration) < time.Now().Unix() {
		return verifyCodeOutcome{id: last.ID, err: eerrs.ErrVerifyCodeExpired.Wrap()}
	}
	if last.Used {
		return verifyCodeOutcome{id: last.ID, err: eerrs.ErrVerifyCodeUsed.Wrap()}
	}
	if last.Code == verifyCode {
		return verifyCodeOutcome{id: last.ID}
	}

	if o.Code.ValidCount > 0 {
		if last.Count >= o.Code.ValidCount {
			return verifyCodeOutcome{
				id:  last.ID,
				err: eerrs.ErrVerifyCodeMaxCount.Wrap(),
			}
		}
		if err := o.Database.UpdateVerifyCodeIncrCount(ctx, last.ID); err != nil {
			return verifyCodeOutcome{id: last.ID, err: err}
		}
	}
	return verifyCodeOutcome{
		id:  last.ID,
		err: eerrs.ErrVerifyCodeNotMatch.Wrap(),
	}
}

func verifyCodeRespFromOutcome(out verifyCodeOutcome) *chat.VerifyCodeResp {
	resp := &chat.VerifyCodeResp{
		NeedVerifyCaptcha: out.needVerifyCaptcha,
		Verified:          out.err == nil,
	}
	if out.err != nil {
		var codeErr errs.CodeError
		if errors.As(out.err, &codeErr) {
			resp.ErrCode = int32(codeErr.Code())
			resp.ErrMsg = codeErr.Msg()
		} else {
			resp.ErrCode = int32(errs.ErrInternalServer.Code())
			resp.ErrMsg = out.err.Error()
		}
	}
	return resp
}

func (o *chatSvr) verifyCode(ctx context.Context, account string, verifyCode string) (string, error) {
	out := o.doVerifyCode(ctx, account, verifyCode)
	return out.id, out.err
}

func (o *chatSvr) VerifyCode(ctx context.Context, req *chat.VerifyCodeReq) (*chat.VerifyCodeResp, error) {
	var account string
	if req.PhoneNumber != "" {
		account = o.verifyCodeJoin(req.AreaCode, req.PhoneNumber)
	} else {
		account = req.Email
	}
	out := o.doVerifyCode(ctx, account, req.VerifyCode)
	return verifyCodeRespFromOutcome(out), nil
}

func (o *chatSvr) genUserID() string {
	const l = 10
	data := make([]byte, l)
	rand.Read(data)
	chars := []byte("0123456789")
	for i := 0; i < len(data); i++ {
		if i == 0 {
			data[i] = chars[1:][data[i]%9]
		} else {
			data[i] = chars[data[i]%10]
		}
	}
	return string(data)
}

func (o *chatSvr) genVerifyCode() string {
	data := make([]byte, o.Code.Len)
	rand.Read(data)
	chars := []byte("0123456789")
	for i := 0; i < len(data); i++ {
		data[i] = chars[data[i]%10]
	}
	return string(data)
}

func (o *chatSvr) RegisterUser(ctx context.Context, req *chat.RegisterUserReq) (*chat.RegisterUserResp, error) {
	isAdmin, err := o.Admin.CheckNilOrAdmin(ctx)
	ctx = o.WithAdminUser(ctx)
	if err != nil {
		log.ZError(ctx, "checkRegisterInfo failed", err)
		return nil, err
	}
	for i := 0; i < 20; i++ {
		if err = o.checkRegisterInfo(ctx, req.User, isAdmin); err != nil {
			if i < 19 && req.User.Nickname != "" && isNicknameAlreadyExistsErr(err) {
				req.User.Nickname = regenerateNicknameSuffix(req.User.Nickname)
				continue
			}
			log.ZError(ctx, "checkRegisterInfo failed", err)
			return nil, err
		}
		break
	}
	var usedInvitationCode bool
	if !isAdmin {
		if !o.AllowRegister {
			log.ZError(ctx, "register user is disabled", errs.ErrNoPermission.WrapMsg("register user is disabled"))
			return nil, errs.ErrNoPermission.WrapMsg("register user is disabled")
		}
		if req.User.UserID != "" {
			log.ZError(ctx, "register user is disabled", errs.ErrNoPermission.WrapMsg("only admin can set user id"))
			return nil, errs.ErrNoPermission.WrapMsg("only admin can set user id")
		}
		if err := o.Admin.CheckRegister(ctx, req.Ip); err != nil {
			log.ZError(ctx, "register user is disabled", err)
			return nil, err
		}

		conf, err := o.Admin.GetConfig(ctx)
		if err != nil {
			log.ZError(ctx, "register user is disabled", err)
			return nil, err
		}
		if val := conf[constant.NeedInvitationCodeRegisterConfigKey]; datautil.Contain(strings.ToLower(val), "1", "true", "yes") {
			usedInvitationCode = true
			if req.InvitationCode == "" {
				log.ZError(ctx, "register user is disabled", errs.ErrArgs.WrapMsg("invitation code is empty"))
				return nil, errs.ErrArgs.WrapMsg("invitation code is empty")
			}
			if err := o.Admin.CheckInvitationCode(ctx, req.InvitationCode); err != nil {
				log.ZError(ctx, "register user is disabled", err)
				return nil, err
			}
		}
		if req.User.PhoneNumber != "" {
			if _, err := o.verifyCode(ctx, o.verifyCodeJoin(req.User.AreaCode, req.User.PhoneNumber), req.VerifyCode); err != nil {
				log.ZError(ctx, "register user is disabled", err)
				return nil, err
			}
		} else if req.User.Email != "" {
			if _, err := o.verifyCode(ctx, req.User.Email, req.VerifyCode); err != nil {
				log.ZError(ctx, "register user is disabled", err)
				return nil, err
			}
		}
	}

	if req.User.UserID == "" {
		for i := 0; i < 20; i++ {
			userID := o.genUserID()
			_, err := o.Database.GetUser(ctx, userID)
			if err == nil {
				continue
			} else if dbutil.IsDBNotFound(err) {
				req.User.UserID = userID
				break
			} else {
				log.ZError(ctx, "register user is disabled", err)
				return nil, err
			}
		}
		if req.User.UserID == "" {
			log.ZError(ctx, "register user is disabled", errs.ErrInternalServer.WrapMsg("gen user id failed"))
			return nil, errs.ErrInternalServer.WrapMsg("gen user id failed")
		}
	} else {
		_, err := o.Database.GetUser(ctx, req.User.UserID)
		if err == nil {
			log.ZError(ctx, "register user is disabled", errs.ErrArgs.WrapMsg("appoint user id already register"))
			return nil, errs.ErrArgs.WrapMsg("appoint user id already register")
		} else if !dbutil.IsDBNotFound(err) {
			log.ZError(ctx, "register user is disabled", err)
			return nil, err
		}
	}
	var (
		credentials  []*chatdb.Credential
		registerType int32
	)

	if req.User.PhoneNumber != "" {
		registerType = constant.PhoneRegister
		credentials = append(credentials, &chatdb.Credential{
			UserID:      req.User.UserID,
			Account:     BuildCredentialPhone(req.User.AreaCode, req.User.PhoneNumber),
			Type:        constant.CredentialPhone,
			AllowChange: true,
		})
	}

	if req.User.Account != "" {
		credentials = append(credentials, &chatdb.Credential{
			UserID:      req.User.UserID,
			Account:     req.User.Account,
			Type:        constant.CredentialAccount,
			AllowChange: true,
		})
		registerType = constant.AccountRegister
	}

	if req.User.Email != "" {
		registerType = constant.EmailRegister
		credentials = append(credentials, &chatdb.Credential{
			UserID:      req.User.UserID,
			Account:     req.User.Email,
			Type:        constant.CredentialEmail,
			AllowChange: true,
		})
	}
	register := &chatdb.Register{
		UserID:      req.User.UserID,
		DeviceID:    req.DeviceID,
		IP:          req.Ip,
		Platform:    constantpb.PlatformID2Name[int(req.Platform)],
		AccountType: "",
		Mode:        constant.UserMode,
		CreateTime:  time.Now(),
	}
	account := &chatdb.Account{
		UserID:         req.User.UserID,
		Password:       req.User.Password,
		OperatorUserID: mcontext.GetOpUserID(ctx),
		ChangeTime:     register.CreateTime,
		CreateTime:     register.CreateTime,
	}

	attribute := &chatdb.Attribute{
		UserID:         req.User.UserID,
		Account:        req.User.Account,
		PhoneNumber:    req.User.PhoneNumber,
		AreaCode:       req.User.AreaCode,
		Email:          req.User.Email,
		Nickname:       req.User.Nickname,
		FirstName:      req.User.FirstName,
		LastName:       req.User.LastName,
		FullName:       BuildFullName(req.User.FirstName, req.User.LastName),
		Remark:         req.User.Remark,
		FaceURL:        req.User.FaceURL,
		Gender:         req.User.Gender,
		Language:       req.User.Language,
		BirthTime:      time.UnixMilli(req.User.Birth),
		ChangeTime:     register.CreateTime,
		CreateTime:     register.CreateTime,
		AllowVibration: constant.DefaultAllowVibration,
		AllowBeep:      constant.DefaultAllowBeep,
		AllowAddFriend: constant.DefaultAllowAddFriend,
		RegisterType:   registerType,
	}
	if err := o.Database.RegisterUser(ctx, register, account, attribute, credentials); err != nil {
		log.ZError(ctx, "register user is disabled", err)
		return nil, err
	}
	if usedInvitationCode {
		if err := o.Admin.UseInvitationCode(ctx, req.User.UserID, req.InvitationCode); err != nil {
			log.ZError(ctx, "UseInvitationCode", err, "userID", req.User.UserID, "invitationCode", req.InvitationCode)
		}
	}
	var resp chat.RegisterUserResp
	if req.AutoLogin {
		chatToken, err := o.Admin.CreateToken(ctx, req.User.UserID, constant.NormalUser)
		if err == nil {
			resp.ChatToken = chatToken.Token
		} else {
			log.ZError(ctx, "Admin CreateToken Failed", err, "userID", req.User.UserID, "platform", req.Platform)
		}
	}
	resp.UserID = req.User.UserID
	return &resp, nil
}

func (o *chatSvr) Login(ctx context.Context, req *chat.LoginReq) (*chat.LoginResp, error) {
	resp := &chat.LoginResp{}
	var (
		err        error
		credential *chatdb.Credential
		acc        string
	)

	switch {
	case req.Account != "":
		acc = req.Account
	case req.PhoneNumber != "":
		if req.AreaCode == "" {
			log.ZError(ctx, "Login Failed", errs.ErrArgs.WrapMsg("area code must be set"), "req", req)
			return nil, errs.ErrArgs.WrapMsg("area code must")
		}
		if !strings.HasPrefix(req.AreaCode, "+") {
			req.AreaCode = "+" + req.AreaCode
		}
		if _, err := strconv.ParseUint(req.AreaCode[1:], 10, 64); err != nil {
			log.ZError(ctx, "Login Failed", errs.ErrArgs.WrapMsg("area code must be number"), "req", req)
			return nil, errs.ErrArgs.WrapMsg("area code must be number")
		}
		acc = BuildCredentialPhone(req.AreaCode, req.PhoneNumber)
	case req.Email != "":
		acc = req.Email
	default:
		return nil, errs.ErrArgs.WrapMsg("account or phone number or email must be set")
	}
	// Signal-like: one phone = one account, no multi-account matching needed.
	credential, err = o.Database.TakeCredentialByAccount(ctx, acc)
	if err != nil {
		if dbutil.IsDBNotFound(err) {
			log.ZError(ctx, "Login Failed", eerrs.ErrAccountNotFound.WrapMsg("user unregistered"), "req", req)
			return nil, eerrs.ErrAccountNotFound.WrapMsg("user unregistered")
		}
		return nil, err
	}
	if err := o.Admin.CheckLogin(ctx, credential.UserID, req.Ip); err != nil {
		log.ZError(ctx, "Login Failed", err, "req", req)
		return nil, err
	}
	var verifyCodeID *string
	if req.Password != "" {
		account, err := o.Database.TakeAccount(ctx, credential.UserID)
		if err != nil {
			log.ZError(ctx, "Login Failed", err, "req", req, "credential", credential)
			return nil, err
		}
		if account.Password != req.Password {
			log.ZError(ctx, "Login Failed", eerrs.ErrPassword.Wrap(), "account", account, "account", acc, "password", req.Password)
			return nil, eerrs.ErrPassword.WrapMsg("password not match")
		}
	} else if req.VerifyCode != "" {
		var account string
		if req.Email == "" {
			account = o.verifyCodeJoin(req.AreaCode, req.PhoneNumber)
		} else {
			account = req.Email
		}
		id, err := o.verifyCode(ctx, account, req.VerifyCode)
		if err != nil {
			log.ZError(ctx, "Login Failed", err, "req", req)
			return nil, err
		}
		if id != "" {
			verifyCodeID = &id
		}
	}

	if _, err := o.Database.TakeUserTotpEnabled(ctx, credential.UserID); err == nil {
		mfaToken := uuid.New().String()
		session := &cache.MFASession{
			UserID:   credential.UserID,
			DeviceID: req.DeviceID,
			Platform: req.Platform,
			IP:       req.Ip,
		}
		if verifyCodeID != nil {
			session.VerifyCodeID = *verifyCodeID
		}
		if err := o.TotpCache.SetMFASession(ctx, mfaToken, session); err != nil {
			log.ZError(ctx, "Login Failed", err, "req", req)
			return nil, err
		}
		expireAt := time.Now().Add(5 * time.Minute).Unix()
		resp.UserID = credential.UserID
		resp.MfaRequired = true
		resp.MfaToken = mfaToken
		resp.MfaTokenExpireAt = expireAt
		return resp, nil
	} else if !dbutil.IsDBNotFound(err) {
		log.ZError(ctx, "Login Failed", err, "req", req)
		return nil, err
	}

	chatToken, err := o.Admin.CreateToken(ctx, credential.UserID, constant.NormalUser)
	if err != nil {
		log.ZError(ctx, "Login Failed", err, "req", req)
		return nil, err
	}
	now := time.Now()
	record := &chatdb.UserLoginRecord{
		UserID:    credential.UserID,
		LoginTime: now,
		IP:        req.Ip,
		DeviceID:  req.DeviceID,
		Platform:  constantpb.PlatformIDToName(int(req.Platform)),
	}
	if err := o.Database.LoginRecord(ctx, record, verifyCodeID); err != nil {
		log.ZError(ctx, "Login Failed", err, "req", req)
		return nil, err
	}
	if err := o.Database.UpsertUserLoginDevice(ctx, &chatdb.UserLoginDevice{
		UserID:     credential.UserID,
		DeviceID:   req.DeviceID,
		PlatformID: req.Platform,
		CreateTime: now,
		UpdateTime: now,
	}); err != nil {
		log.ZError(ctx, "Login Failed", err, "req", req)
		return nil, err
	}
	if verifyCodeID != nil {
		if err := o.Database.DelVerifyCode(ctx, *verifyCodeID); err != nil {
			log.ZError(ctx, "Login Failed", err, "req", req)
			return nil, err
		}
	}
	resp.UserID = credential.UserID
	resp.ChatToken = chatToken.Token
	return resp, nil
}
