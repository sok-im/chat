package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	WalletTraceIDPrefix = "walletTraceId:"
	walletTraceTTL      = 10 * time.Minute
)

type WalletCache interface {
	SetTraceSign(ctx context.Context, traceID, str string) error
	GetTraceSign(ctx context.Context, traceID string) (string, error)
}

type walletCacheRedis struct {
	rdb redis.UniversalClient
}

func NewWalletCache(rdb redis.UniversalClient) WalletCache {
	return &walletCacheRedis{rdb: rdb}
}

func (c *walletCacheRedis) SetTraceSign(ctx context.Context, traceID, str string) error {
	return c.rdb.Set(ctx, WalletTraceIDPrefix+traceID, str, walletTraceTTL).Err()
}

func (c *walletCacheRedis) GetTraceSign(ctx context.Context, traceID string) (string, error) {
	return c.rdb.Get(ctx, WalletTraceIDPrefix+traceID).Result()
}
