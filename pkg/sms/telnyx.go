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

package sms

import (
	"context"
	"fmt"
	"strings"

	"github.com/openimsdk/tools/errs"
	"github.com/openimsdk/tools/log"
	telnyx "github.com/team-telnyx/telnyx-go/v4"
	"github.com/team-telnyx/telnyx-go/v4/option"
)

// TelnyxSMSConfig drives Telnyx Programmable Messaging (github.com/team-telnyx/telnyx-go/v4).
// Set From to an E.164 number (+...) or MessagingProfileID when using a messaging profile.
//
// BodyTemplates maps normalized BCP 47 tags (e.g. default, en, zh-cn) to a printf-style
// format string that includes the code placeholder (e.g. %s).
type TelnyxSMSConfig struct {
	APIKey             string
	From               string
	MessagingProfileID string
	Body               string
	BodyTemplates      map[string]string
	DefaultLanguage    string
	// Client, when non-nil, is used instead of creating one from APIKey and ClientOptions.
	Client        *telnyx.Client
	ClientOptions []option.RequestOption
}

// NewTelnyx builds a Telnyx-backed SMS sender with optional per-language body templates.
func NewTelnyx(cfg TelnyxSMSConfig) (SMS, error) {
	if cfg.Client == nil && cfg.APIKey == "" {
		return nil, errs.New("telnyx apiKey is required")
	}
	if cfg.From == "" && cfg.MessagingProfileID == "" {
		return nil, errs.New("telnyx from or messagingProfileId is required")
	}
	templates, err := mergeTwilioBodyTemplates(cfg.Body, cfg.BodyTemplates)
	if err != nil {
		return nil, err
	}
	var client telnyx.Client
	if cfg.Client != nil {
		client = *cfg.Client
	} else {
		opts := make([]option.RequestOption, 0, 1+len(cfg.ClientOptions))
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
		opts = append(opts, cfg.ClientOptions...)
		client = telnyx.NewClient(opts...)
	}
	return &telnyxSMS{
		client:             client,
		from:               strings.TrimSpace(cfg.From),
		messagingProfileID: strings.TrimSpace(cfg.MessagingProfileID),
		templates:          templates,
		defaultLanguage:    normalizeLangTag(cfg.DefaultLanguage),
	}, nil
}

type telnyxSMS struct {
	client             telnyx.Client
	from               string
	messagingProfileID string
	templates          map[string]string
	defaultLanguage    string
}

func (t *telnyxSMS) Name() string {
	return "telnyx-sms"
}

func (t *telnyxSMS) SendCode(ctx context.Context, areaCode string, phoneNumber string, verifyCode string, language string) error {
	if err := ctx.Err(); err != nil {
		return errs.Wrap(err)
	}
	bodyFmt := pickTwilioBodyFormat(language, t.defaultLanguage, t.templates)
	params := telnyx.MessageSendParams{
		To:   e164Phone(areaCode, phoneNumber),
		Text: telnyx.String(fmt.Sprintf(bodyFmt, verifyCode)),
	}
	if t.from != "" {
		params.From = telnyx.String(t.from)
	} else {
		params.MessagingProfileID = telnyx.String(t.messagingProfileID)
	}
	_, err := t.client.Messages.Send(ctx, params)

	if err != nil {
		log.ZError(ctx, "telnyx send code failed", err, "params", params)
		return err
	}
	log.ZDebug(ctx, "telnyx send code success", "params", params)
	return nil
}
