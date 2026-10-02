package config

import "testing"

func TestLoadValidation(t *testing.T) {
	for k, v := range map[string]string{"TELEGRAM_BOT_TOKEN": "fake", "OWNER_TELEGRAM_ID": "123", "DATABASE_PATH": "/tmp/bot.db", "WEBHOOK_URL": "https://bot.example.com", "WEBHOOK_PATH_SECRET": "path", "WEBHOOK_SECRET_TOKEN": "secret", "GROUP_TIMEZONE": "Europe/Minsk", "LOG_LEVEL": "info", "HTTP_LISTEN_ADDR": ":8080"} {
		t.Setenv(k, v)
	}
	if _, e := Load(); e != nil {
		t.Fatal(e)
	}
	for _, v := range []struct{ k, value string }{{"OWNER_TELEGRAM_ID", "0"}, {"WEBHOOK_URL", ""}, {"WEBHOOK_URL", "http://bot.example.com"}, {"WEBHOOK_URL", "https://bot.example.com/path"}, {"WEBHOOK_URL", "https://user:secret@bot.example.com"}, {"WEBHOOK_PATH_SECRET", "x/y"}, {"WEBHOOK_SECRET_TOKEN", "bad token"}, {"GROUP_TIMEZONE", "invalid"}, {"LOG_LEVEL", "verbose"}, {"HTTP_LISTEN_ADDR", "bad"}} {
		t.Run(v.k+v.value, func(t *testing.T) {
			t.Setenv(v.k, v.value)
			if _, e := Load(); e == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}
