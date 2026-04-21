package chat

import (
	"context"
	"strconv"
	"strings"

	"github.com/openimsdk/chat/pkg/common/db/dbutil"
	table "github.com/openimsdk/chat/pkg/common/db/table/chat"
	"github.com/openimsdk/chat/pkg/eerrs"
	"github.com/openimsdk/chat/pkg/protocol/chat"
	"github.com/openimsdk/chat/pkg/protocol/common"
	"github.com/openimsdk/tools/errs"
	"github.com/openimsdk/tools/log"
	"github.com/openimsdk/tools/utils/datautil"
	"github.com/openimsdk/tools/utils/stringutil"
)

func DbToPbAttribute(attribute *table.Attribute) *common.UserPublicInfo {
	if attribute == nil {
		return nil
	}
	return &common.UserPublicInfo{
		UserID:    attribute.UserID,
		Account:   attribute.Account,
		Email:     attribute.Email,
		Nickname:  attribute.Nickname,
		FaceURL:   attribute.FaceURL,
		Gender:    attribute.Gender,
		Level:     attribute.Level,
		FirstName: attribute.FirstName,
		LastName:  attribute.LastName,
		Remark:    attribute.Remark,
	}
}

func DbToPbAttributes(attributes []*table.Attribute) []*common.UserPublicInfo {
	return datautil.Slice(attributes, DbToPbAttribute)
}

func DbToPbUserFullInfo(attribute *table.Attribute) *common.UserFullInfo {
	return &common.UserFullInfo{
		UserID:           attribute.UserID,
		Password:         "",
		Account:          attribute.Account,
		PhoneNumber:      attribute.PhoneNumber,
		AreaCode:         attribute.AreaCode,
		Email:            attribute.Email,
		Nickname:         attribute.Nickname,
		FirstName:        attribute.FirstName,
		LastName:         attribute.LastName,
		Remark:           attribute.Remark,
		FaceURL:          attribute.FaceURL,
		Gender:           attribute.Gender,
		Level:            attribute.Level,
		Birth:            attribute.BirthTime.UnixMilli(),
		AllowAddFriend:   attribute.AllowAddFriend,
		AllowBeep:        attribute.AllowBeep,
		AllowVibration:   attribute.AllowVibration,
		GlobalRecvMsgOpt: attribute.GlobalRecvMsgOpt,
		RegisterType:     attribute.RegisterType,
		UseSnCode:        attribute.UseSnCode,
	}
}

func DbToPbUserFullInfos(attributes []*table.Attribute) []*common.UserFullInfo {
	return datautil.Slice(attributes, DbToPbUserFullInfo)
}

func BuildCredentialPhone(areaCode, phone string) string {
	return areaCode + " " + phone
}

// checkRegisterInfo validates the registration payload.
// Signal-like: phone number uniqueness is enforced by evicting old accounts at registration time,
// so there is no per-phone account count limit here.
func (o *chatSvr) checkRegisterInfo(ctx context.Context, user *chat.RegisterUserInfo, isAdmin bool) error {
	if user == nil {
		log.ZError(ctx, "checkRegisterInfo failed", errs.ErrArgs.WrapMsg("user is nil"))
		return errs.ErrArgs.WrapMsg("user is nil")
	}
	if user.Email == "" && !(user.PhoneNumber != "" && user.AreaCode != "") && (!isAdmin || user.Account == "") {
		log.ZError(ctx, "checkRegisterInfo failed", errs.ErrArgs.WrapMsg("at least one valid account is required"))
		return errs.ErrArgs.WrapMsg("at least one valid account is required")
	}
	if user.PhoneNumber != "" {
		if !strings.HasPrefix(user.AreaCode, "+") {
			user.AreaCode = "+" + user.AreaCode
		}
		if _, err := strconv.ParseUint(user.AreaCode[1:], 10, 64); err != nil {
			log.ZError(ctx, "checkRegisterInfo failed", errs.ErrArgs.WrapMsg("area code must be number"))
			return errs.ErrArgs.WrapMsg("area code must be number")
		}
		if _, err := strconv.ParseUint(user.PhoneNumber, 10, 64); err != nil {
			log.ZError(ctx, "checkRegisterInfo failed", errs.ErrArgs.WrapMsg("phone number must be number"))
			return errs.ErrArgs.WrapMsg("phone number must be number")
		}
		// Signal-like: no per-phone account limit; existing accounts are evicted during registration.
	}
	if user.Account != "" {
		if !stringutil.IsAlphanumeric(user.Account) {
			log.ZError(ctx, "checkRegisterInfo failed", errs.ErrArgs.WrapMsg("account must be alphanumeric"))
			return errs.ErrArgs.WrapMsg("account must be alphanumeric")
		}
		_, err := o.Database.TakeAttributeByAccount(ctx, user.Account)
		if err == nil {
			log.ZError(ctx, "checkRegisterInfo failed", eerrs.ErrAccountAlreadyRegister.Wrap())
			return eerrs.ErrAccountAlreadyRegister.Wrap()
		} else if !dbutil.IsDBNotFound(err) {
			log.ZError(ctx, "checkRegisterInfo failed", err)
			return err
		}
	}
	if user.Email != "" {
		if !stringutil.IsValidEmail(user.Email) {
			log.ZError(ctx, "checkRegisterInfo failed", errs.ErrArgs.WrapMsg("invalid email"))
			return errs.ErrArgs.WrapMsg("invalid email")
		}
		_, err := o.Database.TakeAttributeByAccount(ctx, user.Email)
		if err == nil {
			log.ZError(ctx, "checkRegisterInfo failed", eerrs.ErrEmailAlreadyRegister.Wrap())
			return eerrs.ErrEmailAlreadyRegister.Wrap()
		} else if !dbutil.IsDBNotFound(err) {
			log.ZError(ctx, "checkRegisterInfo failed", eerrs.ErrEmailAlreadyRegister.Wrap())
			return eerrs.ErrEmailAlreadyRegister.Wrap()
		}
	}
	if user.Nickname != "" {
		_, err := o.Database.TakeAttributeByNickname(ctx, user.Nickname)
		if err == nil {
			log.ZError(ctx, "checkRegisterInfo failed", errs.ErrArgs.WrapMsg("nickname already exists"))
			return errs.ErrArgs.WrapMsg("nickname already exists")
		} else if !dbutil.IsDBNotFound(err) {
			log.ZError(ctx, "checkRegisterInfo failed", err)
			return err
		}
	}
	return nil
}
