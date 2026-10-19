package handler

import "testing"

func TestDecodeEmailHeader(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"plain", "123456 是您的验证码", "123456 是您的验证码"},
		{"folded Q subject", "123456 =?utf-8?Q?=E6=98=AF=E6=82=A8=E7=9A=84=E9=AA=8C?=\r\n =?utf-8?Q?=E8=AF=81=E7=A0=81?=", "123456 是您的验证码"},
		{"B sender", "=?UTF-8?B?5Lit5paH?= <sender@example.com>", "中文 <sender@example.com>"},
		{"latin1", "=?ISO-8859-1?Q?caf=E9?=", "café"},
		{"unknown charset preserved", "=?x-unknown?Q?hello?=", "=?x-unknown?Q?hello?="},
		{"malformed word preserved", "=?utf-8?B?%%%?=", "=?utf-8?B?%%%?="},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decodeEmailHeader(tt.input); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
