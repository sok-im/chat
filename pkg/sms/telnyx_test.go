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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	telnyx "github.com/team-telnyx/telnyx-go/v4"
	"github.com/team-telnyx/telnyx-go/v4/option"
)

func TestTelnyxSendCode(t *testing.T) {
	var got struct {
		From string `json:"from"`
		To   string `json:"to"`
		Text string `json:"text"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("path: got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method: got %s", r.Method)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Fatalf("authorization: got %q", auth)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"id":"msg_test"}}`))
	}))
	defer srv.Close()

	client := telnyx.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(srv.URL),
	)
	sender, err := NewTelnyx(TelnyxSMSConfig{
		From:   "+15551234567",
		Body:   "Code: %s",
		Client: &client,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.SendCode(context.Background(), "1", "8382068492", "654321", "en"); err != nil {
		t.Fatal(err)
	}
	if got.To != "+18382068492" {
		t.Fatalf("to: got %q", got.To)
	}
	if got.From != "+15551234567" {
		t.Fatalf("from: got %q", got.From)
	}
	if got.Text != "Code: 654321" {
		t.Fatalf("text: got %q", got.Text)
	}
}

func TestTelnyxLiveSend(t *testing.T) {
	apiKey := os.Getenv("TELNYX_API_KEY")
	from := os.Getenv("TELNYX_FROM")
	if apiKey == "" || from == "" {
		t.Skip("set TELNYX_API_KEY and TELNYX_FROM to run live Telnyx SMS test")
	}
	sender, err := NewTelnyx(TelnyxSMSConfig{
		APIKey: apiKey,
		From:   from,
		Body:   "Your verification code is: %s",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.SendCode(context.Background(), "1", "8382068492", "123456", "en"); err != nil {
		t.Fatal(err)
	}
}
