package chat

import (
	"context"
	"time"

	"github.com/openimsdk/chat/pkg/common/db/table/chat"
	"github.com/openimsdk/tools/db/mongoutil"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func NewUserTotpRecovery(db *mongo.Database) (chat.UserTotpRecoveryInterface, error) {
	coll := db.Collection(chat.UserTotpRecovery{}.TableName())
	_, err := coll.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{Keys: bson.D{{Key: "user_id", Value: 1}}},
		{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "used", Value: 1}}},
	})
	if err != nil {
		return nil, err
	}
	return &UserTotpRecovery{coll: coll}, nil
}

type UserTotpRecovery struct {
	coll *mongo.Collection
}

func (o *UserTotpRecovery) Create(ctx context.Context, records []*chat.UserTotpRecovery) error {
	if len(records) == 0 {
		return nil
	}
	return mongoutil.InsertMany(ctx, o.coll, records)
}

func (o *UserTotpRecovery) FindUnusedByUserID(ctx context.Context, userID string) ([]*chat.UserTotpRecovery, error) {
	return mongoutil.Find[*chat.UserTotpRecovery](ctx, o.coll, bson.M{
		"user_id": userID,
		"used":    false,
	})
}

func (o *UserTotpRecovery) MarkUsed(ctx context.Context, userID, codeHash string) error {
	return mongoutil.UpdateOne(ctx, o.coll, bson.M{
		"user_id":   userID,
		"code_hash": codeHash,
		"used":      false,
	}, bson.M{"$set": bson.M{
		"used":    true,
		"used_at": time.Now().Unix(),
	}}, false)
}

func (o *UserTotpRecovery) CountUnused(ctx context.Context, userID string) (int64, error) {
	return mongoutil.Count(ctx, o.coll, bson.M{
		"user_id": userID,
		"used":    false,
	})
}

func (o *UserTotpRecovery) DeleteByUserID(ctx context.Context, userID string) error {
	return mongoutil.DeleteMany(ctx, o.coll, bson.M{"user_id": userID})
}
