package chat

import (
	"context"
	"time"

	"github.com/openimsdk/chat/pkg/common/db/table/chat"
	"github.com/openimsdk/tools/db/mongoutil"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func NewUserTotp(db *mongo.Database) (chat.UserTotpInterface, error) {
	coll := db.Collection(chat.UserTotp{}.TableName())
	_, err := coll.Indexes().CreateOne(context.Background(), mongo.IndexModel{
		Keys:    bson.D{{Key: "user_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return nil, err
	}
	return &UserTotp{coll: coll}, nil
}

type UserTotp struct {
	coll *mongo.Collection
}

func (o *UserTotp) TakeEnabled(ctx context.Context, userID string) (*chat.UserTotp, error) {
	return mongoutil.FindOne[*chat.UserTotp](ctx, o.coll, bson.M{
		"user_id": userID,
		"enabled": true,
	})
}

func (o *UserTotp) Upsert(ctx context.Context, record *chat.UserTotp) error {
	now := time.Now()
	if record.CreateTime.IsZero() {
		record.CreateTime = now
	}
	record.UpdateTime = now
	_, err := o.coll.UpdateOne(ctx, bson.M{"user_id": record.UserID}, bson.M{
		"$set": bson.M{
			"secret":      record.Secret,
			"enabled":     record.Enabled,
			"bound_at":    record.BoundAt,
			"update_time": record.UpdateTime,
		},
		"$setOnInsert": bson.M{
			"user_id":     record.UserID,
			"create_time": record.CreateTime,
		},
	}, options.Update().SetUpsert(true))
	return err
}

func (o *UserTotp) DeleteByUserID(ctx context.Context, userID string) error {
	return mongoutil.DeleteMany(ctx, o.coll, bson.M{"user_id": userID})
}
