package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateAlertTarget(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "https", value: "https://hooks.example.test/notify"},
		{name: "local test receiver", value: "http://127.0.0.1:8099/webhook"},
		{name: "missing host", value: "https:///notify", wantErr: true},
		{name: "unsupported scheme", value: "file:///tmp/webhook", wantErr: true},
		{name: "embedded credentials", value: "https://user:pass@example.test/notify", wantErr: true},
		{name: "empty", value: "", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAlertTarget(test.value)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateAlertTarget(%q) error = %v, wantErr %v", test.value, err, test.wantErr)
			}
		})
	}
}

func TestAlertTargetHintMasksSecrets(t *testing.T) {
	got := alertTargetHint("https://open.feishu.cn/open-apis/bot/v2/hook/secret-token?key=secret-query")
	if strings.Contains(got, "secret-token") || strings.Contains(got, "secret-query") {
		t.Fatalf("target hint leaked webhook credentials: %s", got)
	}
	if got != "https://open.feishu.cn/•••" {
		t.Fatalf("unexpected target hint: %s", got)
	}
}

func TestPostAlertMessageWebhookSignature(t *testing.T) {
	t.Setenv("ORP_DATA_KEY", strings.Repeat("a", 64))
	secret := "webhook-signing-secret"
	payload := []byte(`{"eventType":"test","title":"test"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("X-ORP-Event"); got != "test" {
			t.Errorf("X-ORP-Event = %q, want test", got)
		}
		timestamp := r.Header.Get("X-ORP-Timestamp")
		if timestamp == "" {
			t.Error("X-ORP-Timestamp is empty")
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(payload)
		wantSignature := "sha256=" + hexString(mac.Sum(nil))
		if got := r.Header.Get("X-ORP-Signature"); got != wantSignature {
			t.Errorf("X-ORP-Signature = %q, want %q", got, wantSignature)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer server.Close()
	sealedURL, err := orpSeal(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	sealedSecret, err := orpSeal(secret)
	if err != nil {
		t.Fatal(err)
	}
	channel := alertChannelRecord{
		alertChannelView: alertChannelView{ID: 8, Name: "Test webhook", Type: "webhook"},
		TargetEncrypted:  sealedURL,
		SecretEncrypted:  sql.NullString{String: sealedSecret, Valid: true},
	}
	status, response, err := postAlertMessage(context.Background(), channel, payload, tlsAlertMessage{EventType: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusAccepted || response != `{"accepted":true}` {
		t.Fatalf("response = %d %q", status, response)
	}
}

func TestPostAlertMessageFeishuSignature(t *testing.T) {
	t.Setenv("ORP_DATA_KEY", strings.Repeat("b", 64))
	secret := "feishu-signing-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		if body["msg_type"] != "text" {
			t.Errorf("msg_type = %v, want text", body["msg_type"])
		}
		timestamp, _ := body["timestamp"].(string)
		if timestamp == "" {
			t.Error("timestamp is empty")
		}
		stringToSign := timestamp + "\n" + secret
		mac := hmac.New(sha256.New, []byte(stringToSign))
		mac.Write(nil)
		want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		if body["sign"] != want {
			t.Errorf("sign = %v, want %s", body["sign"], want)
		}
		content, _ := body["content"].(map[string]any)
		if !strings.Contains(content["text"].(string), "到期") {
			t.Errorf("unexpected message content: %v", content)
		}
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok"}`))
	}))
	defer server.Close()
	sealedURL, err := orpSeal(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	sealedSecret, err := orpSeal(secret)
	if err != nil {
		t.Fatal(err)
	}
	channel := alertChannelRecord{
		alertChannelView: alertChannelView{ID: 9, Name: "Test Feishu", Type: "feishu"},
		TargetEncrypted:  sealedURL,
		SecretEncrypted:  sql.NullString{String: sealedSecret, Valid: true},
	}
	status, _, err := postAlertMessage(context.Background(), channel, []byte(`{}`), tlsAlertMessage{Title: "证书提醒", Message: "证书即将到期"})
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
}

func TestPostAlertMessageFeishuCard(t *testing.T) {
	t.Setenv("ORP_DATA_KEY", strings.Repeat("d", 64))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		if body["msg_type"] != "interactive" {
			t.Errorf("msg_type = %v, want interactive", body["msg_type"])
		}
		card, ok := body["card"].(map[string]any)
		if !ok {
			t.Fatalf("card payload missing: %#v", body["card"])
		}
		header := card["header"].(map[string]any)
		if header["template"] != "red" {
			t.Errorf("card template = %v, want red for near expiry", header["template"])
		}
		title := header["title"].(map[string]any)
		if title["content"] != "TLS 证书到期提醒：api.example.test" {
			t.Errorf("unexpected card title: %v", title["content"])
		}
		elements := card["elements"].([]any)
		first := elements[0].(map[string]any)
		text := first["text"].(map[string]any)["content"].(string)
		for _, fragment := range []string{"证书详情", "api.example.test", "2026-10-10", "剩余天数：3 天"} {
			if !strings.Contains(text, fragment) {
				t.Errorf("card content missing %q: %s", fragment, text)
			}
		}
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok"}`))
	}))
	defer server.Close()
	sealedURL, err := orpSeal(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	status, _, err := postAlertMessage(context.Background(), alertChannelRecord{
		alertChannelView: alertChannelView{Type: "feishu", MessageFormat: "card"}, TargetEncrypted: sealedURL,
	}, []byte(`{}`), tlsAlertMessage{
		EventType: "tls_expiry", Title: "TLS 证书到期提醒：api.example.test", Message: "TLS 证书即将到期。",
		Certificate: "api.example.test", Domains: "api.example.test", ExpiresAt: "2026-10-10", DaysRemaining: 3, ThresholdDays: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
}

func TestNormalizeAlertMessageFormat(t *testing.T) {
	for _, test := range []struct {
		channelType string
		format      string
		want        string
		wantErr     bool
	}{
		{channelType: "feishu", format: "", want: "text"},
		{channelType: "feishu", format: "card", want: "card"},
		{channelType: "webhook", format: "", want: "text"},
		{channelType: "webhook", format: "card", wantErr: true},
	} {
		got, err := normalizeAlertMessageFormat(test.channelType, test.format)
		if (err != nil) != test.wantErr || got != test.want {
			t.Errorf("normalizeAlertMessageFormat(%q, %q) = %q, %v; want %q, wantErr=%t", test.channelType, test.format, got, err, test.want, test.wantErr)
		}
	}
}

func TestPostAlertMessageFeishuRejectsBusinessAndMalformedResponses(t *testing.T) {
	t.Setenv("ORP_DATA_KEY", strings.Repeat("c", 64))
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "business error", body: `{"code":19001,"msg":"invalid token"}`},
		{name: "malformed response", body: `not-json`},
		{name: "empty response", body: ``},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			sealedURL, err := orpSeal(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = postAlertMessage(context.Background(), alertChannelRecord{
				alertChannelView: alertChannelView{Type: "feishu"}, TargetEncrypted: sealedURL,
			}, []byte(`{}`), tlsAlertMessage{Title: "提醒", Message: "即将到期"})
			if err == nil {
				t.Fatal("postAlertMessage() error = nil, want Feishu response error")
			}
		})
	}
}

func TestCertificateDaysRemainingUsesCalendarDays(t *testing.T) {
	location := time.FixedZone("test", 8*60*60)
	now := time.Date(2026, 10, 7, 23, 30, 0, 0, location)
	if got := certificateDaysRemaining(time.Date(2026, 10, 8, 0, 0, 0, 0, location), now); got != 1 {
		t.Fatalf("remaining days = %d, want 1", got)
	}
	if got := certificateDaysRemaining(time.Date(2026, 10, 6, 0, 0, 0, 0, location), now); got != -1 {
		t.Fatalf("expired days = %d, want -1", got)
	}
}
