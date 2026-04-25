// Copyright © 2023 OpenIM open source community. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package chat

import (
	"context"

	"github.com/openimsdk/chat/pkg/common/db/table/chat"
	"github.com/openimsdk/tools/db/mongoutil"
	"github.com/openimsdk/tools/errs"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func NewUserLoginDevice(db *mongo.Database) (chat.UserLoginDeviceInterface, error) {
	coll := db.Collection("user_login_device")
	_, err := coll.Indexes().CreateOne(context.Background(), mongo.IndexModel{
		Keys: bson.D{
			{Key: "user_id", Value: 1},
			{Key: "device_id", Value: 1},
		},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return nil, errs.Wrap(err)
	}
	return &UserLoginDevice{
		coll: coll,
	}, nil
}

type UserLoginDevice struct {
	coll *mongo.Collection
}

func (o *UserLoginDevice) Upsert(ctx context.Context, device *chat.UserLoginDevice) error {
	return mongoutil.UpdateOne(ctx, o.coll, bson.M{
		"user_id":   device.UserID,
		"device_id": device.DeviceID,
	}, bson.M{
		"$set": bson.M{
			"platform_id": device.PlatformID,
			"update_time": device.UpdateTime,
		},
		"$setOnInsert": bson.M{
			"user_id":     device.UserID,
			"device_id":   device.DeviceID,
			"create_time": device.CreateTime,
		},
	}, false, options.Update().SetUpsert(true))
}
