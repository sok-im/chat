package chat

import (
	"context"

	"github.com/openimsdk/chat/pkg/common/db/table/chat"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func NewCaptcha(db *mongo.Database) (chat.CaptchaInterface, error) {
	return &Captcha{
		coll: db.Collection("captcha"),
	}, nil
}

type Captcha struct {
	coll *mongo.Collection
}

func (o *Captcha) Consume(ctx context.Context, captchaID string) (bool, error) {
	res, err := o.coll.DeleteOne(ctx, bson.M{
		"captcha_id": captchaID,
	})
	if err != nil {
		return false, err
	}
	return res.DeletedCount > 0, nil
}
