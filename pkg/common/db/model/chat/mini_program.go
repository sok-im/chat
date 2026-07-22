package chat

import (
	"context"

	"github.com/openimsdk/tools/db/mongoutil"
	"github.com/openimsdk/tools/errs"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/openimsdk/chat/pkg/common/db/table/chat"
)

const miniProgramStatusOnline = "online"

func NewMiniProgramEntry(db *mongo.Database) (chat.MiniProgramEntryInterface, error) {
	coll := db.Collection(chat.MiniProgramEntry{}.TableName())
	_, err := coll.Indexes().CreateOne(context.Background(), mongo.IndexModel{
		Keys:    bson.D{{Key: "id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return nil, errs.Wrap(err)
	}
	return &MiniProgramEntry{coll: coll}, nil
}

type MiniProgramEntry struct {
	coll *mongo.Collection
}

func (o *MiniProgramEntry) Take(ctx context.Context, id string) (*chat.MiniProgramEntry, error) {
	return mongoutil.FindOne[*chat.MiniProgramEntry](ctx, o.coll, bson.M{"id": id})
}

func (o *MiniProgramEntry) FindByIDs(ctx context.Context, ids []string) ([]*chat.MiniProgramEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	return mongoutil.Find[*chat.MiniProgramEntry](ctx, o.coll, bson.M{"id": bson.M{"$in": ids}})
}

func (o *MiniProgramEntry) FindOnline(ctx context.Context, categoryID, keyword string) ([]*chat.MiniProgramEntry, int64, error) {
	filter := bson.M{"status": miniProgramStatusOnline}
	if categoryID != "" {
		filter["categoryIds"] = categoryID
	}
	if keyword != "" {
		filter["$or"] = []bson.M{
			{"name": bson.M{"$regex": keyword, "$options": "i"}},
			{"description": bson.M{"$regex": keyword, "$options": "i"}},
		}
	}
	opt := options.Find().SetSort(bson.D{{Key: "sort", Value: 1}, {Key: "id", Value: 1}})
	entries, err := mongoutil.Find[*chat.MiniProgramEntry](ctx, o.coll, filter, opt)
	if err != nil {
		return nil, 0, err
	}
	var maxRevision int64
	for _, e := range entries {
		if e.Revision > maxRevision {
			maxRevision = e.Revision
		}
	}
	return entries, maxRevision, nil
}

func NewMiniProgramCategory(db *mongo.Database) (chat.MiniProgramCategoryInterface, error) {
	coll := db.Collection(chat.MiniProgramCategory{}.TableName())
	_, err := coll.Indexes().CreateOne(context.Background(), mongo.IndexModel{
		Keys:    bson.D{{Key: "id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return nil, errs.Wrap(err)
	}
	return &MiniProgramCategory{coll: coll}, nil
}

type MiniProgramCategory struct {
	coll *mongo.Collection
}

func (o *MiniProgramCategory) FindAll(ctx context.Context) ([]*chat.MiniProgramCategory, error) {
	opt := options.Find().SetSort(bson.D{{Key: "sort", Value: 1}, {Key: "id", Value: 1}})
	return mongoutil.Find[*chat.MiniProgramCategory](ctx, o.coll, bson.M{}, opt)
}

func NewMiniProgramFavorite(db *mongo.Database) (chat.MiniProgramFavoriteInterface, error) {
	coll := db.Collection(chat.MiniProgramFavorite{}.TableName())
	_, err := coll.Indexes().CreateOne(context.Background(), mongo.IndexModel{
		Keys:    bson.D{{Key: "userId", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return nil, errs.Wrap(err)
	}
	return &MiniProgramFavorite{coll: coll}, nil
}

type MiniProgramFavorite struct {
	coll *mongo.Collection
}

func (o *MiniProgramFavorite) Take(ctx context.Context, userID string) (*chat.MiniProgramFavorite, error) {
	return mongoutil.FindOne[*chat.MiniProgramFavorite](ctx, o.coll, bson.M{"userId": userID})
}

func (o *MiniProgramFavorite) Upsert(ctx context.Context, record *chat.MiniProgramFavorite) error {
	update := bson.M{"$set": bson.M{
		"entryIds":  record.EntryIDs,
		"revision":  record.Revision,
		"updatedAt": record.UpdatedAt,
	}}
	return mongoutil.UpdateOne(ctx, o.coll, bson.M{"userId": record.UserID}, update, true)
}

func NewMiniProgramRecent(db *mongo.Database) (chat.MiniProgramRecentInterface, error) {
	coll := db.Collection(chat.MiniProgramRecentEntry{}.TableName())
	_, err := coll.Indexes().CreateOne(context.Background(), mongo.IndexModel{
		Keys:    bson.D{{Key: "userId", Value: 1}, {Key: "entryId", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return nil, errs.Wrap(err)
	}
	return &MiniProgramRecent{coll: coll}, nil
}

type MiniProgramRecent struct {
	coll *mongo.Collection
}

func (o *MiniProgramRecent) FindByUser(ctx context.Context, userID string, limit int64) ([]*chat.MiniProgramRecentEntry, error) {
	opt := options.Find().SetSort(bson.D{{Key: "lastOpenedAt", Value: -1}})
	if limit > 0 {
		opt.SetLimit(limit)
	}
	return mongoutil.Find[*chat.MiniProgramRecentEntry](ctx, o.coll, bson.M{"userId": userID}, opt)
}

func (o *MiniProgramRecent) Upsert(ctx context.Context, record *chat.MiniProgramRecentEntry) error {
	update := bson.M{"$set": bson.M{"lastOpenedAt": record.LastOpenedAt}}
	return mongoutil.UpdateOne(ctx, o.coll, bson.M{"userId": record.UserID, "entryId": record.EntryID}, update, true)
}

func (o *MiniProgramRecent) Count(ctx context.Context, userID string) (int64, error) {
	return mongoutil.Count(ctx, o.coll, bson.M{"userId": userID})
}

func (o *MiniProgramRecent) TrimOldest(ctx context.Context, userID string, keep int64) error {
	if keep < 0 {
		return nil
	}
	opt := options.Find().
		SetSort(bson.D{{Key: "lastOpenedAt", Value: -1}}).
		SetSkip(keep).
		SetProjection(bson.M{"entryId": 1})
	stale, err := mongoutil.Find[*chat.MiniProgramRecentEntry](ctx, o.coll, bson.M{"userId": userID}, opt)
	if err != nil {
		return err
	}
	if len(stale) == 0 {
		return nil
	}
	ids := make([]string, 0, len(stale))
	for _, s := range stale {
		ids = append(ids, s.EntryID)
	}
	return mongoutil.DeleteMany(ctx, o.coll, bson.M{"userId": userID, "entryId": bson.M{"$in": ids}})
}
