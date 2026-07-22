package cache

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	mpTicketPrefix     = "mp:ticket:"       // mp:ticket:{jti} -> TicketClaims JSON
	mpTicketUserPrefix = "mp:ticket:user:"  // set of jti per userId
	mpTicketEntryPref  = "mp:ticket:entry:" // set of jti per entryId
	mpTicketAppPrefix  = "mp:ticket:app:"   // set of jti per finclipAppId
	mpIdempotencyPref  = "mp:idem:"         // mp:idem:{userId}:{key} -> response JSON

	mpTicketIndexTTL = 24 * time.Hour
	mpIdempotencyTTL = 10 * time.Minute
)

// MiniProgramTicketClaims are the persisted claims for a short-lived business ticket.
// It never contains phone number, IM token, avatar or wallet identifiers.
type MiniProgramTicketClaims struct {
	JTI          string   `json:"jti"`
	UserID       string   `json:"sub"`
	FinclipAppID string   `json:"aud"`
	EntryID      string   `json:"entryId"`
	InstallID    string   `json:"installId,omitempty"`
	Scope        []string `json:"scope"`
	IssuedAt     int64    `json:"iat"`
	ExpiresAt    int64    `json:"exp"`
}

type MiniProgramCache interface {
	SetTicket(ctx context.Context, claims *MiniProgramTicketClaims, ttl time.Duration) error
	GetTicket(ctx context.Context, jti string) (*MiniProgramTicketClaims, error)
	// RevokeTickets removes tickets by any of the provided dimensions and returns
	// the number of tickets removed.
	RevokeTickets(ctx context.Context, userID, installID, entryID, finclipAppID, jti string) (int64, error)

	GetIdempotent(ctx context.Context, userID, key string) ([]byte, bool, error)
	SetIdempotent(ctx context.Context, userID, key string, response []byte) error
}

type miniProgramCacheRedis struct {
	rdb redis.UniversalClient
}

func NewMiniProgramCache(rdb redis.UniversalClient) MiniProgramCache {
	return &miniProgramCacheRedis{rdb: rdb}
}

func (c *miniProgramCacheRedis) SetTicket(ctx context.Context, claims *MiniProgramTicketClaims, ttl time.Duration) error {
	data, err := json.Marshal(claims)
	if err != nil {
		return err
	}
	pipe := c.rdb.TxPipeline()
	pipe.Set(ctx, mpTicketPrefix+claims.JTI, data, ttl)
	if claims.UserID != "" {
		key := mpTicketUserPrefix + claims.UserID
		pipe.SAdd(ctx, key, claims.JTI)
		pipe.Expire(ctx, key, mpTicketIndexTTL)
	}
	if claims.EntryID != "" {
		key := mpTicketEntryPref + claims.EntryID
		pipe.SAdd(ctx, key, claims.JTI)
		pipe.Expire(ctx, key, mpTicketIndexTTL)
	}
	if claims.FinclipAppID != "" {
		key := mpTicketAppPrefix + claims.FinclipAppID
		pipe.SAdd(ctx, key, claims.JTI)
		pipe.Expire(ctx, key, mpTicketIndexTTL)
	}
	_, err = pipe.Exec(ctx)
	return err
}

func (c *miniProgramCacheRedis) GetTicket(ctx context.Context, jti string) (*MiniProgramTicketClaims, error) {
	data, err := c.rdb.Get(ctx, mpTicketPrefix+jti).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, err
	}
	var claims MiniProgramTicketClaims
	if err := json.Unmarshal([]byte(data), &claims); err != nil {
		return nil, err
	}
	return &claims, nil
}

func (c *miniProgramCacheRedis) RevokeTickets(ctx context.Context, userID, installID, entryID, finclipAppID, jti string) (int64, error) {
	jtis := make(map[string]struct{})
	if jti != "" {
		jtis[jti] = struct{}{}
	}
	collect := func(setKey string) error {
		members, err := c.rdb.SMembers(ctx, setKey).Result()
		if err != nil && err != redis.Nil {
			return err
		}
		for _, m := range members {
			jtis[m] = struct{}{}
		}
		return nil
	}
	if userID != "" {
		if err := collect(mpTicketUserPrefix + userID); err != nil {
			return 0, err
		}
	}
	if entryID != "" {
		if err := collect(mpTicketEntryPref + entryID); err != nil {
			return 0, err
		}
	}
	if finclipAppID != "" {
		if err := collect(mpTicketAppPrefix + finclipAppID); err != nil {
			return 0, err
		}
	}
	var revoked int64
	for j := range jtis {
		// When both userID and installID are provided, only revoke tickets of
		// that install instance.
		if userID != "" && installID != "" {
			claims, err := c.GetTicket(ctx, j)
			if err != nil {
				return revoked, err
			}
			if claims == nil || claims.InstallID != installID {
				continue
			}
		}
		n, err := c.rdb.Del(ctx, mpTicketPrefix+j).Result()
		if err != nil {
			return revoked, err
		}
		revoked += n
	}
	// Clean up index sets that were fully targeted.
	if userID != "" && installID == "" {
		_ = c.rdb.Del(ctx, mpTicketUserPrefix+userID).Err()
	}
	if entryID != "" {
		_ = c.rdb.Del(ctx, mpTicketEntryPref+entryID).Err()
	}
	if finclipAppID != "" {
		_ = c.rdb.Del(ctx, mpTicketAppPrefix+finclipAppID).Err()
	}
	return revoked, nil
}

func (c *miniProgramCacheRedis) GetIdempotent(ctx context.Context, userID, key string) ([]byte, bool, error) {
	if key == "" {
		return nil, false, nil
	}
	data, err := c.rdb.Get(ctx, mpIdempotencyPref+userID+":"+key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}

func (c *miniProgramCacheRedis) SetIdempotent(ctx context.Context, userID, key string, response []byte) error {
	if key == "" {
		return nil
	}
	return c.rdb.Set(ctx, mpIdempotencyPref+userID+":"+key, response, mpIdempotencyTTL).Err()
}
