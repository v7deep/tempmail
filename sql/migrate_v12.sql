-- v12: featured mailboxes and opt-in catch-all configuration.
ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS is_favorite BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX IF NOT EXISTS idx_mailboxes_account_favorite ON mailboxes (account_id, created_at DESC) WHERE is_favorite = TRUE;
INSERT INTO app_settings (key, value) VALUES ('auto_create_unknown_mailboxes', 'false') ON CONFLICT DO NOTHING;

INSERT INTO app_settings (key,value) VALUES ('telegram_enabled','false'),('telegram_bot_token',''),('telegram_chat_id','') ON CONFLICT DO NOTHING;
