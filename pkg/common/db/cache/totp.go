package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	totpPendingKeyPrefix = "totp:pending:"
	totpMFAKeyPrefix     = "totp:mfa:"
	totpMFAFailPrefix    = "totp:mfa:fail:"

	totpPendingTTL = 10 * time.Minute
	totpMFATTL     = 5 * time.Minute
	totpMFAFailTTL = 5 * time.Minute
	totpMFAFailMax = 5
)

type MFASession struct {
	UserID       string `json:"userID"`
	DeviceID     string `json:"deviceID"`
	Platform     int32  `json:"platform"`
	IP           string `json:"ip"`
	VerifyCodeID string `json:"verifyCodeID,omitempty"`
}

type TotpCache interface {
	SetPendingSecret(ctx context.Context, userID, secret string) error
	GetPendingSecret(ctx context.Context, userID string) (string, error)
	DeletePendingSecret(ctx context.Context, userID string) error

	SetMFASession(ctx context.Context, mfaToken string, session *MFASession) error
	GetMFASession(ctx context.Context, mfaToken string) (*MFASession, error)
	DeleteMFASession(ctx context.Context, mfaToken string) error

	IncrMFAFailCount(ctx context.Context, mfaToken string) (int64, error)
	DeleteMFAFailCount(ctx context.Context, mfaToken string) error
}

type totpCacheRedis struct {
	rdb redis.UniversalClient
}

func NewTotpCache(rdb redis.UniversalClient) TotpCache {
	return &totpCacheRedis{rdb: rdb}
}

func (c *totpCacheRedis) SetPendingSecret(ctx context.Context, userID, secret string) error {
	return c.rdb.Set(ctx, totpPendingKeyPrefix+userID, secret, totpPendingTTL).Err()
}

func (c *totpCacheRedis) GetPendingSecret(ctx context.Context, userID string) (string, error) {
	return c.rdb.Get(ctx, totpPendingKeyPrefix+userID).Result()
}

func (c *totpCacheRedis) DeletePendingSecret(ctx context.Context, userID string) error {
	return c.rdb.Del(ctx, totpPendingKeyPrefix+userID).Err()
}

func (c *totpCacheRedis) SetMFASession(ctx context.Context, mfaToken string, session *MFASession) error {
	data, err := json.Marshal(session)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, totpMFAKeyPrefix+mfaToken, data, totpMFATTL).Err()
}

func (c *totpCacheRedis) GetMFASession(ctx context.Context, mfaToken string) (*MFASession, error) {
	data, err := c.rdb.Get(ctx, totpMFAKeyPrefix+mfaToken).Result()
	if err != nil {
		return nil, err
	}
	var session MFASession
	if err := json.Unmarshal([]byte(data), &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (c *totpCacheRedis) DeleteMFASession(ctx context.Context, mfaToken string) error {
	return c.rdb.Del(ctx, totpMFAKeyPrefix+mfaToken).Err()
}

func (c *totpCacheRedis) IncrMFAFailCount(ctx context.Context, mfaToken string) (int64, error) {
	key := totpMFAFailPrefix + mfaToken
	count, err := c.rdb.Incr(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	if count == 1 {
		_ = c.rdb.Expire(ctx, key, totpMFAFailTTL).Err()
	}
	return count, nil
}

func (c *totpCacheRedis) DeleteMFAFailCount(ctx context.Context, mfaToken string) error {
	return c.rdb.Del(ctx, totpMFAFailPrefix+mfaToken).Err()
}

func IsMFAFailLimit(count int64) bool {
	return count > totpMFAFailMax
}

func TotpPendingKey(userID string) string {
	return fmt.Sprintf("%s%s", totpPendingKeyPrefix, userID)
}
