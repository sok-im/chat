package chat

import (
	"context"

	"github.com/openimsdk/chat/pkg/common/db/table/chat"
	"github.com/openimsdk/tools/db/mongoutil"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type mongoCaptcha struct {
	CaptchaID string `bson:"captcha_id"`
}

func NewCaptcha(db *mongo.Database) (chat.CaptchaInterface, error) {
	return &Captcha{coll: db.Collection(chat.Captcha{}.TableName())}, nil
}

type Captcha struct {
	coll *mongo.Collection
}

func (o *Captcha) Take(ctx context.Context, captchaID string) (*chat.Captcha, error) {
	captcha, err := mongoutil.FindOne[*mongoCaptcha](ctx, o.coll, bson.M{"captcha_id": captchaID})
	if err != nil {
		return nil, err
	}
	return &chat.Captcha{CaptchaID: captcha.CaptchaID}, nil
}

func (o *Captcha) Delete(ctx context.Context, captchaID string) error {
	return mongoutil.DeleteOne(ctx, o.coll, bson.M{"captcha_id": captchaID})
}
