package chat

import "context"

type CaptchaInterface interface {
	Consume(ctx context.Context, captchaID string) (bool, error)
}
