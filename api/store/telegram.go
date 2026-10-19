package store

import (
 "context"
 "encoding/json"
 "fmt"
 "log"
 "net/http"
 "net/url"
 "strings"
 "time"

 "github.com/google/uuid"
)

type telegramEvent struct {
 MailboxID uuid.UUID
 Recipient string
 Sender string
 Subject string
}
var telegramHTTP = &http.Client{Timeout: 8 * time.Second}

// StartTelegramNotifier starts one bounded, best-effort worker. Delivery never waits
// for Telegram's network response; a full queue drops notifications, not emails.
func (s *Store) StartTelegramNotifier(ctx context.Context) {
 if s.telegramQueue != nil { return }
 s.telegramQueue = make(chan telegramEvent, 256)
 go func() {
  for {
   select {
   case <-ctx.Done(): return
   case e := <-s.telegramQueue:
    if err := s.notifyFeaturedMailbox(ctx, e); err != nil {
     log.Printf("[telegram] notification failed for mailbox %s: %v", e.MailboxID, err)
    }
   }
  }
 }()
}
func (s *Store) enqueueTelegram(events []DeliveredEmail, sender, subject string) {
 if s.telegramQueue == nil { return }
 for _, e := range events {
  if !e.Delivered { continue }
  select {
  case s.telegramQueue <- telegramEvent{MailboxID:e.MailboxID, Recipient:e.Recipient, Sender:sender, Subject:subject}:
  default: log.Printf("[telegram] notification queue full; dropped for mailbox %s", e.MailboxID)
  }
 }
}
func (s *Store) notifyFeaturedMailbox(ctx context.Context, e telegramEvent) error {
 var starred bool
 err := s.pool.QueryRow(ctx, "SELECT is_favorite FROM mailboxes WHERE id=$1", e.MailboxID).Scan(&starred)
 if err != nil || !starred { return err }
 enabled, _ := s.GetSetting(ctx, "telegram_enabled")
 if enabled != "true" { return nil }
 token, err := s.GetSetting(ctx, "telegram_bot_token")
 if err != nil { return err }
 chat, err := s.GetSetting(ctx, "telegram_chat_id")
 if err != nil { return err }
 if token == "" || chat == "" { return nil }
 message := "📬 TempMail 精选邮箱收到新邮件\n邮箱: "+e.Recipient+"\n发件人: "+e.Sender+"\n主题: "+e.Subject
 if len([]rune(message)) > 3500 { message = string([]rune(message)[:3500]) }
 return sendTelegram(ctx, token, chat, message)
}
func sendTelegram(ctx context.Context, token, chat, message string) error {
 if len(token) > 200 || strings.ContainsAny(token, "/?\r\n ") || chat == "" { return fmt.Errorf("invalid telegram settings") }
 vals := url.Values{"chat_id":{chat},"text":{message},"disable_web_page_preview":{"true"}}
 req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+token+"/sendMessage", strings.NewReader(vals.Encode()))
 if err != nil { return err }
 req.Header.Set("Content-Type","application/x-www-form-urlencoded")
 res, err := telegramHTTP.Do(req)
 if err != nil { return fmt.Errorf("telegram request failed") } // do not log URL containing bot token
 defer res.Body.Close()
 var reply struct { OK bool `json:"ok"` }
 _ = json.NewDecoder(res.Body).Decode(&reply)
 if res.StatusCode != http.StatusOK || !reply.OK { return fmt.Errorf("telegram returned HTTP %d",res.StatusCode) }
 return nil
}
func (s *Store) TestTelegram(ctx context.Context) error {
 token, err := s.GetSetting(ctx,"telegram_bot_token"); if err != nil {return err}
 chat, err := s.GetSetting(ctx,"telegram_chat_id"); if err != nil {return err}
 if token == "" || chat == "" {return fmt.Errorf("configure bot token and chat ID first")}
 return sendTelegram(ctx,token,chat,"✅ TempMail Telegram 测试消息：通知配置成功")
}
