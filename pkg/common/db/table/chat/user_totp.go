package chat

import (
	"context"
	"time"
)

type UserTotp struct {
	UserID     string    `bson:"user_id"`
	Secret     string    `bson:"secret"`
	Enabled    bool      `bson:"enabled"`
	BoundAt    int64     `bson:"bound_at"`
	CreateTime time.Time `bson:"create_time"`
	UpdateTime time.Time `bson:"update_time"`
}

func (UserTotp) TableName() string {
	return "user_totp"
}

type UserTotpInterface interface {
	TakeEnabled(ctx context.Context, userID string) (*UserTotp, error)
	Upsert(ctx context.Context, record *UserTotp) error
	DeleteByUserID(ctx context.Context, userID string) error
}

type UserTotpRecovery struct {
	UserID     string    `bson:"user_id"`
	CodeHash   string    `bson:"code_hash"`
	Used       bool      `bson:"used"`
	UsedAt     int64     `bson:"used_at"`
	CreateTime time.Time `bson:"create_time"`
}

func (UserTotpRecovery) TableName() string {
	return "user_totp_recovery"
}

type UserTotpRecoveryInterface interface {
	Create(ctx context.Context, records []*UserTotpRecovery) error
	FindUnusedByUserID(ctx context.Context, userID string) ([]*UserTotpRecovery, error)
	MarkUsed(ctx context.Context, userID, codeHash string) error
	CountUnused(ctx context.Context, userID string) (int64, error)
	DeleteByUserID(ctx context.Context, userID string) error
}
