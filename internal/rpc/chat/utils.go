package chat

import (
	"context"
	"crypto/rand"
	"fmt"
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
		Language:  attribute.Language,
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
		Language:         attribute.Language,
	}
}

func DbToPbUserFullInfos(attributes []*table.Attribute) []*common.UserFullInfo {
	return datautil.Slice(attributes, DbToPbUserFullInfo)
}

func BuildCredentialPhone(areaCode, phone string) string {
	return areaCode + " " + phone
}

func BuildFullName(firstName, lastName string) string {
	if firstName == "" {
		return lastName
	}
	if lastName == "" {
		return firstName
	}
	return strings.TrimSpace(firstName + " " + lastName)
}

// checkRegisterInfo validates the registration payload.
func (o *chatSvr) checkRegisterInfo(ctx context.Context, user *chat.RegisterUserInfo, _ bool) error {
	if user == nil {
		log.ZError(ctx, "checkRegisterInfo failed", errs.ErrArgs.WrapMsg("user is nil"))
		return errs.ErrArgs.WrapMsg("user is nil")
	}
	// Removed the "at least one valid account" check to allow registration
	// without PhoneNumber, Email, or Account (e.g., for userIdent-based registration).
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
		attrs, err := o.Database.FindAttributeByPhone(ctx, user.AreaCode, user.PhoneNumber)
		if err != nil {
			log.ZError(ctx, "checkRegisterInfo failed", err)
			return err
		}
		if len(attrs) > 0 {
			log.ZError(ctx, "checkRegisterInfo failed", eerrs.ErrPhoneAlreadyRegister.Wrap())
			return eerrs.ErrPhoneAlreadyRegister.Wrap()
		}
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

func isNicknameAlreadyExistsErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "nickname already exists")
}

func genNicknameSuffix() string {
	data := make([]byte, 4)
	if _, err := rand.Read(data); err != nil {
		return "0000"
	}
	n := int(data[0])<<24 | int(data[1])<<16 | int(data[2])<<8 | int(data[3])
	if n < 0 {
		n = -n
	}
	return fmt.Sprintf("%04d", n%10000)
}

func regenerateNicknameSuffix(nickname string) string {
	idx := strings.LastIndex(nickname, ".")
	if idx > 0 && len(nickname) == idx+5 {
		suffix := nickname[idx+1:]
		if len(suffix) == 4 {
			for _, c := range suffix {
				if c < '0' || c > '9' {
					return nickname + "." + genNicknameSuffix()
				}
			}
			return nickname[:idx+1] + genNicknameSuffix()
		}
	}
	return nickname + "." + genNicknameSuffix()
}
