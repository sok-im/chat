package chat

import (
	"context"
	"time"

	"github.com/openimsdk/chat/pkg/common/mctx"
	"github.com/openimsdk/chat/pkg/common/rtc"
	"github.com/openimsdk/chat/pkg/protocol/admin"
	"github.com/openimsdk/chat/pkg/protocol/chat"
	"github.com/openimsdk/tools/db/mongoutil"
	"github.com/openimsdk/tools/discovery"
	"github.com/openimsdk/tools/errs"
	"github.com/openimsdk/tools/mw"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/openimsdk/chat/pkg/common/config"
	"github.com/openimsdk/chat/pkg/common/db/database"
	"github.com/openimsdk/chat/pkg/email"
	chatClient "github.com/openimsdk/chat/pkg/rpclient/chat"
	"github.com/openimsdk/chat/pkg/sms"
)

type Config struct {
	RpcConfig     config.Chat
	RedisConfig   config.Redis
	MongodbConfig config.Mongo
	Discovery     config.Discovery
	Share         config.Share
}

func Start(ctx context.Context, config *Config, client discovery.SvcDiscoveryRegistry, server *grpc.Server) error {
	if len(config.Share.ChatAdmin) == 0 {
		return errs.New("share chat admin not configured")
	}
	mgocli, err := mongoutil.NewMongoDB(ctx, config.MongodbConfig.Build())
	if err != nil {
		return err
	}
	var srv chatSvr
	switch config.RpcConfig.VerifyCode.Phone.Use {
	case "ali":
		ali := config.RpcConfig.VerifyCode.Phone.Ali
		srv.SMS, err = sms.NewAli(ali.Endpoint, ali.AccessKeyID, ali.AccessKeySecret, ali.SignName, ali.VerificationCodeTemplateCode)
		if err != nil {
			return err
		}
	case "twilio":
		tw := config.RpcConfig.VerifyCode.Phone.Twilio
		srv.SMS, err = sms.NewTwilio(sms.TwilioSMSConfig{
			AccountSID:      tw.AccountSid,
			AuthToken:       tw.AuthToken,
			From:            tw.From,
			Body:            tw.Body,
			BodyTemplates:   tw.BodyTemplates,
			DefaultLanguage: tw.DefaultLanguage,
		})
		if err != nil {
			return err
		}
	case "telnyx":
		tx := config.RpcConfig.VerifyCode.Phone.Telnyx
		srv.SMS, err = sms.NewTelnyx(sms.TelnyxSMSConfig{
			APIKey:             tx.APIKey,
			From:               tx.From,
			MessagingProfileID: tx.MessagingProfileID,
			Body:               tx.Body,
			BodyTemplates:      tx.BodyTemplates,
			DefaultLanguage:    tx.DefaultLanguage,
		})
		if err != nil {
			return err
		}
	}
	if mail := config.RpcConfig.VerifyCode.Mail; mail.Enable {
		srv.Mail = email.NewMail(mail.SMTPAddr, mail.SMTPPort, mail.SenderMail, mail.SenderAuthorizationCode, mail.Title)
	}
	srv.Database, err = database.NewChatDatabase(mgocli)
	if err != nil {
		return err
	}
	conn, err := client.GetConn(ctx, config.Discovery.RpcService.Admin, grpc.WithTransportCredentials(insecure.NewCredentials()), mw.GrpcClient())
	if err != nil {
		return err
	}
	srv.Admin = chatClient.NewAdminClient(admin.NewAdminClient(conn))
	srv.Code = verifyCode{
		UintTime:          time.Duration(config.RpcConfig.VerifyCode.UintTime) * time.Second,
		MaxCount:          config.RpcConfig.VerifyCode.MaxCount,
		NeedVerifyCaptchaCount: config.RpcConfig.VerifyCode.NeedVerifyCaptchaCount,
		ValidCount:        config.RpcConfig.VerifyCode.ValidCount,
		SuperCode:         config.RpcConfig.VerifyCode.SuperCode,
		ValidTime:         time.Duration(config.RpcConfig.VerifyCode.ValidTime) * time.Second,
		Len:               config.RpcConfig.VerifyCode.Len,
	}
	srv.Livekit = rtc.NewLiveKit(config.RpcConfig.LiveKit.Key, config.RpcConfig.LiveKit.Secret, config.RpcConfig.LiveKit.URL)
	srv.AllowRegister = config.RpcConfig.AllowRegister
	srv.MaxAccountsPerPhone = config.RpcConfig.MaxAccountsPerPhone
	chat.RegisterChatServer(server, &srv)
	return nil
}

type chatSvr struct {
	chat.UnimplementedChatServer
	Database            database.ChatDatabaseInterface
	Admin               *chatClient.AdminClient
	SMS                 sms.SMS
	Mail                email.Mail
	Code                verifyCode
	Livekit             *rtc.LiveKit
	ChatAdminUserID     string
	AllowRegister       bool
	MaxAccountsPerPhone int
}

func (o *chatSvr) WithAdminUser(ctx context.Context) context.Context {
	return mctx.WithAdminUser(ctx, o.ChatAdminUserID)
}

type verifyCode struct {
	UintTime          time.Duration // sec
	MaxCount          int
	NeedVerifyCaptchaCount int
	ValidCount        int
	SuperCode         string
	ValidTime         time.Duration
	Len               int
}
