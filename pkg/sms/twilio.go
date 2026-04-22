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

	"github.com/twilio/twilio-go"
	twilioApi "github.com/twilio/twilio-go/rest/api/v2010"
	"github.com/openimsdk/tools/errs"
)

const twilioFallbackBody = "Your verification code is: %s"

// TwilioSMSConfig drives Twilio Programmable Messaging (github.com/twilio/twilio-go).
// From must be E.164 (+...) or Messaging Service SID (MG...).
//
// BodyTemplates maps normalized BCP 47 tags (e.g. default, en, zh-cn) to a printf-style
// format string that includes the code placeholder (e.g. %s). Special key "default" is used
// when the client language is unknown or empty (after DefaultLanguage is tried).
// Legacy single field Body is merged as the "default" template when no "default" key exists.
type TwilioSMSConfig struct {
	AccountSID      string
	AuthToken       string
	From            string
	Body            string
	BodyTemplates   map[string]string
	DefaultLanguage string
}

// NewTwilio builds a Twilio-backed SMS sender with optional per-language body templates.
func NewTwilio(cfg TwilioSMSConfig) (SMS, error) {
	if cfg.AccountSID == "" || cfg.AuthToken == "" || cfg.From == "" {
		return nil, errs.New("twilio accountSid, authToken and from are required")
	}
	templates, err := mergeTwilioBodyTemplates(cfg.Body, cfg.BodyTemplates)
	if err != nil {
		return nil, err
	}
	client := twilio.NewRestClientWithParams(twilio.ClientParams{
		Username: cfg.AccountSID,
		Password: cfg.AuthToken,
	})
	return &twilioSMS{
		client:          client,
		fromValue:       cfg.From,
		templates:       templates,
		defaultLanguage: normalizeLangTag(cfg.DefaultLanguage),
	}, nil
}

type twilioSMS struct {
	client          *twilio.RestClient
	fromValue       string
	templates       map[string]string
	defaultLanguage string
}

func (t *twilioSMS) Name() string {
	return "twilio-sms"
}

func (t *twilioSMS) SendCode(ctx context.Context, areaCode string, phoneNumber string, verifyCode string, language string) error {
	if err := ctx.Err(); err != nil {
		return errs.Wrap(err)
	}
	bodyFmt := pickTwilioBodyFormat(language, t.defaultLanguage, t.templates)
	to := e164Phone(areaCode, phoneNumber)
	params := &twilioApi.CreateMessageParams{}
	params.SetTo(to)
	if strings.HasPrefix(t.fromValue, "MG") {
		params.SetMessagingServiceSid(t.fromValue)
	} else {
		params.SetFrom(t.fromValue)
	}
	params.SetBody(fmt.Sprintf(bodyFmt, verifyCode))
	_, err := t.client.Api.CreateMessage(params)
	return errs.Wrap(err)
}

func mergeTwilioBodyTemplates(legacyBody string, bodyTemplates map[string]string) (map[string]string, error) {
	out := make(map[string]string)
	for k, v := range bodyTemplates {
		nk := normalizeLangTag(k)
		if nk == "" {
			continue
		}
		out[nk] = v
	}
	if legacyBody != "" {
		if _, ok := out["default"]; !ok {
			out["default"] = legacyBody
		}
	}
	if len(out) == 0 {
		out["default"] = twilioFallbackBody
	}
	for k, v := range out {
		if !strings.Contains(v, "%") {
			return nil, errs.New("twilio template must contain a printf verb for the code", "lang", k)
		}
	}
	return out, nil
}

func pickTwilioBodyFormat(clientLang, defaultLang string, templates map[string]string) string {
	if s := resolveTwilioTemplate(clientLang, templates); s != "" {
		return s
	}
	if s := resolveTwilioTemplate(defaultLang, templates); s != "" {
		return s
	}
	if s, ok := templates["default"]; ok {
		return s
	}
	return twilioFallbackBody
}

func resolveTwilioTemplate(lang string, templates map[string]string) string {
	n := normalizeLangTag(lang)
	if n == "" {
		return ""
	}
	if t, ok := templates[n]; ok {
		return t
	}
	if i := strings.Index(n, "-"); i > 0 {
		base := n[:i]
		if t, ok := templates[base]; ok {
			return t
		}
	}
	return ""
}

func normalizeLangTag(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "_", "-"))
	if s == "" {
		return ""
	}
	return strings.ToLower(s)
}

func e164Phone(areaCode, phoneNumber string) string {
	ac := strings.TrimSpace(strings.TrimPrefix(areaCode, "+"))
	pn := strings.TrimSpace(strings.TrimPrefix(phoneNumber, "+"))
	if ac == "" {
		if strings.HasPrefix(phoneNumber, "+") {
			return phoneNumber
		}
		return "+" + pn
	}
	return "+" + ac + pn
}
