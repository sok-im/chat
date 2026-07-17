package chat

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/openimsdk/tools/errs"
	"github.com/redis/go-redis/v9"

	chatdb "github.com/openimsdk/chat/pkg/common/db/table/chat"
	"github.com/openimsdk/chat/pkg/common/wallet"
	"github.com/openimsdk/chat/pkg/protocol/chat"
)

func (o *chatSvr) GetWalletSignKey(ctx context.Context, _ *chat.GetWalletSignKeyReq) (*chat.GetWalletSignKeyResp, error) {
	str, err := wallet.RandomString(10)
	if err != nil {
		return nil, err
	}
	traceID := uuid.New().String()
	if err := o.WalletCache.SetTraceSign(ctx, traceID, str); err != nil {
		return nil, err
	}
	return &chat.GetWalletSignKeyResp{Str: str, TraceId: traceID}, nil
}

func (o *chatSvr) CheckWalletAddress(ctx context.Context, req *chat.CheckWalletAddressReq) (*chat.CheckWalletAddressResp, error) {
	str, err := o.loadWalletSignStr(ctx, req.TraceId)
	if err != nil {
		return nil, err
	}
	addrs, err := wallet.VerifyChains(str, protoChain(req.Evm), protoChain(req.Tron), protoChain(req.Bitcoin), protoChain(req.Solana))
	if err != nil {
		return nil, err
	}
	record, err := o.Database.SelectUserLoginWalletByAddress(ctx, addrs.Evm, addrs.Tron, addrs.Bitcoin, addrs.Solana)
	if err != nil {
		return nil, err
	}
	isRegister := int32(0)
	if record != nil {
		isRegister = 1
	}
	return &chat.CheckWalletAddressResp{IsRegister: isRegister}, nil
}

func (o *chatSvr) WalletAppLogin(ctx context.Context, req *chat.WalletAppLoginReq) (*chat.WalletAppLoginResp, error) {
	if req.Platform < 1 {
		return nil, errs.ErrArgs.WrapMsg("platform must be at least 1")
	}
	str, err := o.loadWalletSignStr(ctx, req.TraceId)
	if err != nil {
		return nil, err
	}
	addrs, err := wallet.VerifyChains(str, protoChain(req.Evm), protoChain(req.Tron), protoChain(req.Bitcoin), protoChain(req.Solana))
	if err != nil {
		return nil, err
	}
	record, err := o.Database.SelectUserLoginWalletByAddress(ctx, addrs.Evm, addrs.Tron, addrs.Bitcoin, addrs.Solana)
	if err != nil {
		return nil, err
	}
	if record != nil {
		loginResp, err := o.Login(ctx, &chat.LoginReq{
			Platform: req.Platform,
			DeviceID: req.DeviceID,
			Ip:       req.Ip,
			Uid:      record.UserID,
		})
		if err != nil {
			return nil, err
		}
		if loginResp.MfaRequired {
			return nil, wallet.ErrCommonFail
		}
		now := time.Now()
		if err := o.Database.UpdateUserLoginWalletAddresses(ctx, record.ID, addrs.Evm, addrs.Tron, addrs.Bitcoin, addrs.Solana, now); err != nil {
			return nil, err
		}
		return &chat.WalletAppLoginResp{
			ChatToken: loginResp.ChatToken,
			UserID:    record.UserID,
		}, nil
	}

	firstName := req.FirstName
	lastName := req.LastName
	language := req.Language
	if firstName == "" && lastName == "" {
		firstName = "SokIM"
		n, randErr := rand.Int(rand.Reader, big.NewInt(26))
		if randErr != nil {
			lastName = "UserA"
		} else {
			lastName = "User" + string(rune('A'+n.Int64()))
		}
	}
	if language == "" {
		language = "en"
	}
	registerReq := &chat.RegisterUserReq{
		InvitationCode: req.InvitationCode,
		Ip:             req.Ip,
		DeviceID:       req.DeviceID,
		Platform:       req.Platform,
		AutoLogin:      true,
		User: &chat.RegisterUserInfo{
			Nickname:  strings.Split(uuid.New().String(), "-")[0],
			Gender:    req.Gender,
			FirstName: firstName,
			LastName:  lastName,
			Language:  language,
		},
	}
	registerResp, err := o.RegisterUser(ctx, registerReq)
	if err != nil {
		return nil, err
	}
	// RegisterUser may regenerate the nickname on collision; read back the final value.
	nickname := registerReq.User.Nickname
	now := time.Now()
	if err := o.Database.CreateUserLoginWallet(ctx, &chatdb.UserLoginWallet{
		ID:             uuid.New().String(),
		UserID:         registerResp.UserID,
		EvmAddress:     addrs.Evm,
		TronAddress:    addrs.Tron,
		BitcoinAddress: addrs.Bitcoin,
		SolanaAddress:  addrs.Solana,
		UpdatedTime:    now,
	}); err != nil {
		return nil, err
	}
	return &chat.WalletAppLoginResp{
		ChatToken: registerResp.ChatToken,
		UserID:    registerResp.UserID,
		NewUser:   true,
		UserInfo: &chat.RegisterUserInfo{
			UserID:    registerResp.UserID,
			Nickname:  nickname,
			Gender:    req.Gender,
			FirstName: firstName,
			LastName:  lastName,
			Language:  language,
		},
	}, nil
}

func (o *chatSvr) loadWalletSignStr(ctx context.Context, traceID string) (string, error) {
	str, err := o.WalletCache.GetTraceSign(ctx, traceID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", wallet.ErrSignKey
		}
		return "", err
	}
	if str == "" {
		return "", wallet.ErrSignKey
	}
	return str, nil
}

func protoChain(p *chat.WalletChainParams) *wallet.ChainParams {
	if p == nil {
		return nil
	}
	return &wallet.ChainParams{
		Address: p.Address,
		Sign:    p.Sign,
		MsgHash: p.MsgHash,
	}
}
