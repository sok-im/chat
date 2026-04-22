package sms

import "context"

type SMS interface {
	Name() string
	// language is a BCP 47 tag (e.g. zh-CN, en); implementations that do not localize may ignore it.
	SendCode(ctx context.Context, areaCode string, phoneNumber string, verifyCode string, language string) error
}
