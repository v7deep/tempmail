package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDeliverAllDecodesMIMEHeadersAndPreservesRaw(t *testing.T) {
	const wantSubject = "123456 是您的验证码"
	const wantSender = "中文 <sender@example.com>"
	const wantBody = "你好，验证码为 123456。"
	raw := []byte("From: =?UTF-8?B?5Lit5paH?= <sender@example.com>\r\n" +
		"Subject: 123456 =?utf-8?Q?=E6=98=AF=E6=82=A8=E7=9A=84=E9=AA=8C?=\r\n" +
		" =?utf-8?Q?=E8=AF=81=E7=A0=81?=\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		base64.StdEncoding.EncodeToString([]byte(wantBody)) + "\r\n")

	for _, recipients := range [][]string{{"one@example.com"}, {"one@example.com", "two@example.com"}} {
		t.Run(strings.Join(recipients, ","), func(t *testing.T) {
			received := make(chan deliverPayload, 1)
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					deliverPayload
					Recipients []string `json:"recipients"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Errorf("decode request: %v", err)
					http.Error(w, "invalid payload", http.StatusBadRequest)
					return
				}
				received <- payload.deliverPayload
				w.Header().Set("Content-Type", "application/json")
				if len(recipients) > 1 {
					if r.URL.Path != "/internal/deliver-batch" {
						t.Errorf("unexpected batch path: %s", r.URL.Path)
					}
					var response batchResponse
					for _, recipient := range payload.Recipients {
						response.Results = append(response.Results, batchResult{Recipient: recipient, Status: "delivered"})
					}
					_ = json.NewEncoder(w).Encode(response)
				} else {
					_, _ = w.Write([]byte(`{"status":"delivered"}`))
				}
			}))
			defer ts.Close()
			client := newAPIHTTPClient()
			defer client.CloseIdleConnections()
			var replies bytes.Buffer
			sess := &session{
				srv:        &server{apiURL: ts.URL, httpClient: client},
				w:          bufio.NewWriter(&replies),
				recipients: recipients,
			}
			sess.deliverAll(context.Background(), raw)
			select {
			case got := <-received:
				if got.Subject != wantSubject || got.Sender != wantSender || got.BodyText != wantBody {
					t.Errorf("decoded message: subject=%q sender=%q body=%q", got.Subject, got.Sender, got.BodyText)
				}
				if got.Raw != string(raw) {
					t.Error("raw MIME message changed")
				}
			default:
				t.Fatal("delivery API was not called")
			}
			if got := strings.Count(replies.String(), "250 2.0.0"); got != len(recipients) {
				t.Errorf("expected %d accepted recipients, got replies %q", len(recipients), replies.String())
			}
		})
	}
}

func TestExtractBodiesMIMEEncodings(t *testing.T) {
	encodedHTML := base64.StdEncoding.EncodeToString([]byte("<p>你好</p>"))
	tests := []struct {
		name, raw, text, html string
	}{
		{"plain", "Content-Type: text/plain\r\n\r\nhello", "hello", ""},
		{"base64 text", "Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: BASE64\r\n\r\n5L2g5aW9", "你好", ""},
		{"quoted printable text", "Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n=E4=BD=A0=E5=A5=BD=3D41", "你好=41", ""},
		{"base64 html", "Content-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n" + encodedHTML, "", "<p>你好</p>"},
		{"multipart alternative", "Content-Type: multipart/alternative; boundary=b\r\n\r\n" +
			"--b\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n=E4=BD=A0=E5=A5=BD=3D41\r\n" +
			"--b\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n" + encodedHTML + "\r\n--b--\r\n", "你好=41", "<p>你好</p>"},
		{"nested with text attachment", "Content-Type: multipart/mixed; boundary=outer\r\n\r\n" +
			"--outer\r\nContent-Type: text/plain\r\nContent-Disposition: attachment; filename=note.txt\r\n\r\nattachment\r\n" +
			"--outer\r\nContent-Type: multipart/alternative; boundary=inner\r\n\r\n" +
			"--inner\r\nContent-Type: text/plain\r\nContent-Transfer-Encoding: base64\r\n\r\n5L2g5aW9\r\n--inner--\r\n" +
			"--outer--\r\n", "你好", ""},
		{"malformed base64 preserved", "Content-Type: text/plain\r\nContent-Transfer-Encoding: base64\r\n\r\n%%%", "%%%", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := mail.ReadMessage(strings.NewReader(tt.raw))
			if err != nil {
				t.Fatal(err)
			}
			text, html := extractBodies(msg)
			if text != tt.text || html != tt.html {
				t.Errorf("got text=%q html=%q; want text=%q html=%q", text, html, tt.text, tt.html)
			}
		})
	}
}

func TestDeliverDrainsResponseAndReusesConnection(t *testing.T) {
	var newConnections atomic.Int64
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"delivered","email_id":"00000000-0000-0000-0000-000000000001"}`))
	}))
	ts.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			newConnections.Add(1)
		}
	}
	ts.Start()
	defer ts.Close()

	client := newAPIHTTPClient()
	defer client.CloseIdleConnections()
	srv := &server{apiURL: ts.URL, httpClient: client}
	payload := deliverPayload{Recipient: "test@example.com", Raw: "hello"}
	for i := 0; i < 25; i++ {
		if err := srv.deliver(context.Background(), payload); err != nil {
			t.Fatalf("deliver %d: %v", i, err)
		}
	}

	if got := newConnections.Load(); got != 1 {
		t.Fatalf("expected one reused HTTP connection, got %d", got)
	}
}

func TestValidateBatchResults(t *testing.T) {
	recipients := []string{"a@example.com", "b@example.com"}
	tests := []struct {
		name    string
		results []batchResult
		wantErr bool
	}{
		{
			name: "valid",
			results: []batchResult{
				{Recipient: "a@example.com", Status: "delivered"},
				{Recipient: "b@example.com", Status: "discarded"},
			},
		},
		{
			name:    "missing result",
			results: []batchResult{{Recipient: "a@example.com", Status: "delivered"}},
			wantErr: true,
		},
		{
			name: "wrong order",
			results: []batchResult{
				{Recipient: "b@example.com", Status: "delivered"},
				{Recipient: "a@example.com", Status: "delivered"},
			},
			wantErr: true,
		},
		{
			name: "invalid status",
			results: []batchResult{
				{Recipient: "a@example.com", Status: "maybe"},
				{Recipient: "b@example.com", Status: "delivered"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBatchResults(recipients, tt.results)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateBatchResults() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
