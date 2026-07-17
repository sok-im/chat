package chat

import (
	"context"
	"time"

	"github.com/openimsdk/tools/db/mongoutil"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/openimsdk/chat/pkg/common/db/table/chat"
)

func NewUserLoginWallet(db *mongo.Database) (chat.UserLoginWalletInterface, error) {
	coll := db.Collection(chat.UserLoginWallet{}.TableName())
	return &UserLoginWallet{coll: coll}, nil
}

type UserLoginWallet struct {
	coll *mongo.Collection
}

func (o *UserLoginWallet) FindByAddress(ctx context.Context, field string, address string) (*chat.UserLoginWallet, error) {
	if address == "" {
		return nil, nil
	}
	return mongoutil.FindOne[*chat.UserLoginWallet](ctx, o.coll, bson.M{field: address})
}

func (o *UserLoginWallet) Create(ctx context.Context, record *chat.UserLoginWallet) error {
	return mongoutil.InsertOne(ctx, o.coll, record)
}

func (o *UserLoginWallet) UpdateAddresses(ctx context.Context, id string, evm, tron, bitcoin, solana string, updatedTime time.Time) error {
	update := bson.M{"updatedTime": updatedTime}
	if evm != "" {
		update["evmAddress"] = evm
	}
	if tron != "" {
		update["tronAddress"] = tron
	}
	if bitcoin != "" {
		update["bitcoinAddress"] = bitcoin
	}
	if solana != "" {
		update["solanaAddress"] = solana
	}
	return mongoutil.UpdateOne(ctx, o.coll, bson.M{"_id": id}, bson.M{"$set": update}, false)
}
