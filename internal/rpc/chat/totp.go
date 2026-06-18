package chat

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	constantpb "github.com/openimsdk/protocol/constant"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	"github.com/openimsdk/chat/pkg/common/constant"
	"github.com/openimsdk/chat/pkg/common/db/cache"
	"github.com/openimsdk/chat/pkg/common/db/dbutil"
	chatdb "github.com/openimsdk/chat/pkg/common/db/table/chat"
	"github.com/openimsdk/chat/pkg/eerrs"
	"github.com/openimsdk/chat/pkg/protocol/chat"
	"github.com/openimsdk/tools/errs"
	"github.com/openimsdk/tools/log"
)

const (
	totpIssuer          = "SOK-IM"
	recoveryCodeCount   = 8
	recoveryCodeWarning = 3
)

// GetTotpSecret generates a temporary TOTP secret for the user and stores it in Redis.
func (o *chatSvr) GetTotpSecret(ctx context.Context, req *chat.GetTotpSecretReq) (*chat.GetTotpSecretResp, error) {
	if req.UserID == "" {
		return nil, errs.ErrArgs.WrapMsg("userID is required")
	}
	// Reject if already bound
	if _, err := o.Database.TakeUserTotpEnabled(ctx, req.UserID); err == nil {
		return nil, eerrs.ErrTotpAlreadyBound.Wrap()
	} else if !dbutil.IsDBNotFound(err) {
		log.ZError(ctx, "GetTotpSecret TakeUserTotpEnabled", err, "userID", req.UserID)
		return nil, err
	}

	// Get account name for the OTP URI (use userID as fallback)
	accountName := req.UserID
	if attr, err := o.Database.TakeAttributeByUserID(ctx, req.UserID); err == nil {
		if attr.PhoneNumber != "" {
			accountName = attr.AreaCode + attr.PhoneNumber
		} else if attr.Email != "" {
			accountName = attr.Email
		} else if attr.Account != "" {
			accountName = attr.Account
		}
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: accountName,
		Period:      30,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		log.ZError(ctx, "GetTotpSecret generate key failed", err)
		return nil, errs.ErrInternalServer.WrapMsg("generate totp key failed")
	}

	secret := key.Secret()
	if err := o.TotpCache.SetPendingSecret(ctx, req.UserID, secret); err != nil {
		log.ZError(ctx, "GetTotpSecret SetPendingSecret", err)
		return nil, err
	}

	expireAt := time.Now().Add(10 * time.Minute).Unix()
	return &chat.GetTotpSecretResp{
		Secret:     secret,
		OtpAuthUrl: key.URL(),
		ExpireAt:   expireAt,
	}, nil
}

// BindTotp confirms TOTP binding: verifies the user's first code against the pending secret,
// then persists the secret and generates recovery codes.
func (o *chatSvr) BindTotp(ctx context.Context, req *chat.BindTotpReq) (*chat.BindTotpResp, error) {
	if req.UserID == "" {
		return nil, errs.ErrArgs.WrapMsg("userID is required")
	}
	if req.TotpCode == "" {
		return nil, errs.ErrArgs.WrapMsg("totpCode is required")
	}

	secret, err := o.TotpCache.GetPendingSecret(ctx, req.UserID)
	if err != nil {
		log.ZError(ctx, "BindTotp GetPendingSecret", err, "userID", req.UserID)
		return nil, eerrs.ErrTotpSecretNotFound.Wrap()
	}

	valid, err := totp.ValidateCustom(req.TotpCode, secret, time.Now(), totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil || !valid {
		return nil, eerrs.ErrTotpCodeInvalid.Wrap()
	}

	now := time.Now()
	record := &chatdb.UserTotp{
		UserID:     req.UserID,
		Secret:     secret,
		Enabled:    true,
		BoundAt:    now.Unix(),
		CreateTime: now,
		UpdateTime: now,
	}
	if err := o.Database.UpsertUserTotp(ctx, record); err != nil {
		log.ZError(ctx, "BindTotp UpsertUserTotp", err)
		return nil, err
	}

	// Generate 8 recovery codes
	plainCodes, dbRecords, err := generateRecoveryCodes(req.UserID, recoveryCodeCount)
	if err != nil {
		log.ZError(ctx, "BindTotp generateRecoveryCodes", err)
		return nil, errs.ErrInternalServer.WrapMsg("generate recovery codes failed")
	}
	if err := o.Database.CreateTotpRecoveryCodes(ctx, dbRecords); err != nil {
		log.ZError(ctx, "BindTotp CreateTotpRecoveryCodes", err)
		return nil, err
	}

	_ = o.TotpCache.DeletePendingSecret(ctx, req.UserID)

	return &chat.BindTotpResp{RecoveryCodes: plainCodes}, nil
}

// VerifyTotp is the second login step: validate TOTP/recovery code against a mfaToken session.
func (o *chatSvr) VerifyTotp(ctx context.Context, req *chat.VerifyTotpReq) (*chat.VerifyTotpResp, error) {
	if req.MfaToken == "" {
		return nil, errs.ErrArgs.WrapMsg("mfaToken is required")
	}
	if req.TotpCode == "" {
		return nil, errs.ErrArgs.WrapMsg("totpCode is required")
	}

	session, err := o.TotpCache.GetMFASession(ctx, req.MfaToken)
	if err != nil {
		return nil, eerrs.ErrMfaTokenExpired.Wrap()
	}

	// Check fail count (brute-force protection)
	failCount, err := o.TotpCache.IncrMFAFailCount(ctx, req.MfaToken)
	if err != nil {
		log.ZError(ctx, "VerifyTotp IncrMFAFailCount", err)
		return nil, err
	}
	if cache.IsMFAFailLimit(failCount) {
		_ = o.TotpCache.DeleteMFASession(ctx, req.MfaToken)
		return nil, eerrs.ErrTotpVerifyTooMany.Wrap()
	}

	userTotp, err := o.Database.TakeUserTotpEnabled(ctx, session.UserID)
	if err != nil {
		if dbutil.IsDBNotFound(err) {
			return nil, eerrs.ErrTotpNotBound.Wrap()
		}
		return nil, err
	}

	// Try as a recovery code (8-char alphanumeric) first, then as a TOTP code
	codeOK := false
	usedRecoveryCode := false

	if len(req.TotpCode) > 6 {
		// Treat as recovery code
		unusedCodes, err := o.Database.FindUnusedTotpRecoveryCodes(ctx, session.UserID)
		if err != nil {
			return nil, err
		}
		if len(unusedCodes) == 0 {
			return nil, eerrs.ErrTotpRecoveryUsedUp.Wrap()
		}
		normalised := normaliseRecoveryCode(req.TotpCode)
		for _, rc := range unusedCodes {
			if bcrypt.CompareHashAndPassword([]byte(rc.CodeHash), []byte(normalised)) == nil {
				codeOK = true
				if err := o.Database.MarkTotpRecoveryCodeUsed(ctx, session.UserID, rc.CodeHash); err != nil {
					log.ZError(ctx, "VerifyTotp MarkTotpRecoveryCodeUsed", err)
					return nil, err
				}
				usedRecoveryCode = true
				break
			}
		}
	} else {
		valid, err := totp.ValidateCustom(req.TotpCode, userTotp.Secret, time.Now(), totp.ValidateOpts{
			Period:    30,
			Skew:      1,
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		})
		if err == nil && valid {
			codeOK = true
		}
	}

	if !codeOK {
		return nil, eerrs.ErrTotpCodeInvalid.Wrap()
	}

	// Success: clean up fail counter and mfaToken
	_ = o.TotpCache.DeleteMFAFailCount(ctx, req.MfaToken)
	_ = o.TotpCache.DeleteMFASession(ctx, req.MfaToken)

	chatToken, err := o.Admin.CreateToken(ctx, session.UserID, constant.NormalUser)
	if err != nil {
		log.ZError(ctx, "VerifyTotp CreateToken", err)
		return nil, err
	}

	platform := req.Platform
	if platform == 0 && session.Platform != 0 {
		platform = session.Platform
	}

	now := time.Now()
	record := &chatdb.UserLoginRecord{
		UserID:    session.UserID,
		LoginTime: now,
		IP:        session.IP,
		DeviceID:  session.DeviceID,
		Platform:  constantpb.PlatformIDToName(int(platform)),
	}
	var vcID *string
	if session.VerifyCodeID != "" {
		vcID = &session.VerifyCodeID
	}
	if err := o.Database.LoginRecord(ctx, record, vcID); err != nil {
		log.ZError(ctx, "VerifyTotp LoginRecord", err)
		return nil, err
	}
	if err := o.Database.UpsertUserLoginDevice(ctx, &chatdb.UserLoginDevice{
		UserID:     session.UserID,
		DeviceID:   session.DeviceID,
		PlatformID: platform,
		CreateTime: now,
		UpdateTime: now,
	}); err != nil {
		log.ZError(ctx, "VerifyTotp UpsertUserLoginDevice", err)
		return nil, err
	}

	resp := &chat.VerifyTotpResp{
		ChatToken: chatToken.Token,
		UserID:    session.UserID,
	}

	// Warn if recovery codes are running low
	if usedRecoveryCode {
		remaining, _ := o.Database.CountUnusedTotpRecoveryCodes(ctx, session.UserID)
		if remaining < recoveryCodeWarning {
			log.ZWarn(ctx, "VerifyTotp recovery codes running low", nil, "userID", session.UserID, "remaining", remaining)
		}
	}

	return resp, nil
}

// GetTotpStatus returns the TOTP binding status for a user.
func (o *chatSvr) GetTotpStatus(ctx context.Context, req *chat.GetTotpStatusReq) (*chat.GetTotpStatusResp, error) {
	if req.UserID == "" {
		return nil, errs.ErrArgs.WrapMsg("userID is required")
	}

	record, err := o.Database.TakeUserTotpEnabled(ctx, req.UserID)
	if err != nil {
		if dbutil.IsDBNotFound(err) {
			return &chat.GetTotpStatusResp{Enabled: false}, nil
		}
		return nil, err
	}

	remaining, err := o.Database.CountUnusedTotpRecoveryCodes(ctx, req.UserID)
	if err != nil {
		log.ZError(ctx, "GetTotpStatus CountUnusedTotpRecoveryCodes", err)
		return nil, err
	}

	return &chat.GetTotpStatusResp{
		Enabled:                record.Enabled,
		BoundAt:                record.BoundAt,
		RecoveryCodesRemaining: remaining,
	}, nil
}

// UnbindTotp removes TOTP binding after verifying the current TOTP or recovery code.
func (o *chatSvr) UnbindTotp(ctx context.Context, req *chat.UnbindTotpReq) (*chat.UnbindTotpResp, error) {
	if req.UserID == "" {
		return nil, errs.ErrArgs.WrapMsg("userID is required")
	}
	if req.TotpCode == "" {
		return nil, errs.ErrArgs.WrapMsg("totpCode is required")
	}

	userTotp, err := o.Database.TakeUserTotpEnabled(ctx, req.UserID)
	if err != nil {
		if dbutil.IsDBNotFound(err) {
			return nil, eerrs.ErrTotpNotBound.Wrap()
		}
		return nil, err
	}

	codeOK := false
	if len(req.TotpCode) > 6 {
		unusedCodes, err := o.Database.FindUnusedTotpRecoveryCodes(ctx, req.UserID)
		if err != nil {
			return nil, err
		}
		normalised := normaliseRecoveryCode(req.TotpCode)
		for _, rc := range unusedCodes {
			if bcrypt.CompareHashAndPassword([]byte(rc.CodeHash), []byte(normalised)) == nil {
				codeOK = true
				break
			}
		}
	} else {
		valid, err := totp.ValidateCustom(req.TotpCode, userTotp.Secret, time.Now(), totp.ValidateOpts{
			Period:    30,
			Skew:      1,
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		})
		if err == nil && valid {
			codeOK = true
		}
	}

	if !codeOK {
		return nil, eerrs.ErrTotpCodeInvalid.Wrap()
	}

	if err := o.Database.DeleteUserTotp(ctx, req.UserID); err != nil {
		log.ZError(ctx, "UnbindTotp DeleteUserTotp", err)
		return nil, err
	}
	if err := o.Database.DeleteTotpRecoveryCodes(ctx, req.UserID); err != nil {
		log.ZError(ctx, "UnbindTotp DeleteTotpRecoveryCodes", err)
		return nil, err
	}

	return &chat.UnbindTotpResp{}, nil
}

// generateRecoveryCodes creates n plain-text recovery codes and their bcrypt hashes.
func generateRecoveryCodes(userID string, n int) ([]string, []*chatdb.UserTotpRecovery, error) {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const codeLen = 8

	plainCodes := make([]string, 0, n)
	records := make([]*chatdb.UserTotpRecovery, 0, n)
	now := time.Now()

	for i := 0; i < n; i++ {
		b := make([]byte, codeLen)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, err
		}
		for j := range b {
			b[j] = charset[int(b[j])%len(charset)]
		}
		plain := fmt.Sprintf("%s-%s", string(b[:4]), string(b[4:]))
		plainCodes = append(plainCodes, plain)

		hash, err := bcrypt.GenerateFromPassword([]byte(string(b)), bcrypt.DefaultCost)
		if err != nil {
			return nil, nil, err
		}
		records = append(records, &chatdb.UserTotpRecovery{
			UserID:     userID,
			CodeHash:   string(hash),
			Used:       false,
			CreateTime: now,
		})
	}
	return plainCodes, records, nil
}

// normaliseRecoveryCode strips dashes so "A1B2-C3D4" and "A1B2C3D4" both work.
func normaliseRecoveryCode(code string) string {
	result := make([]byte, 0, len(code))
	for i := 0; i < len(code); i++ {
		if code[i] != '-' {
			result = append(result, code[i])
		}
	}
	return string(result)
}
