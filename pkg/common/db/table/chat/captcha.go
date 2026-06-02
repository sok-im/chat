package chat

import "context"

type Captcha struct {
	CaptchaID string `bson:"captcha_id"`
}

func (Captcha) TableName() string {
	return "captcha"
}

type CaptchaInterface interface {
	Take(ctx context.Context, captchaID string) (*Captcha, error)
	Delete(ctx context.Context, captchaID string) error
}
