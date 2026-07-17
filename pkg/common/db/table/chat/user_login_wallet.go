package chat

import (
	"context"
	"time"
)

// UserLoginWallet maps Mongo collection user_login_wallet (Java entity field names).
type UserLoginWallet struct {
	ID             string    `bson:"_id,omitempty"`
	UserID         string    `bson:"userId"`
	EvmAddress     string    `bson:"evmAddress"`
	TronAddress    string    `bson:"tronAddress"`
	BitcoinAddress string    `bson:"bitcoinAddress"`
	SolanaAddress  string    `bson:"solanaAddress"`
	UpdatedTime    time.Time `bson:"updatedTime"`
}

func (UserLoginWallet) TableName() string {
	return "user_login_wallet"
}

type UserLoginWalletInterface interface {
	FindByAddress(ctx context.Context, field string, address string) (*UserLoginWallet, error)
	Create(ctx context.Context, record *UserLoginWallet) error
	UpdateAddresses(ctx context.Context, id string, evm, tron, bitcoin, solana string, updatedTime time.Time) error
}
