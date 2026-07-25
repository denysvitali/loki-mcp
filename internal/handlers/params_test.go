package handlers

import (
	"testing"

	"github.com/spf13/viper"
)

// TestLokiParams_applyDefaults pins the viper-backed fallback behaviour of
// applyDefaults. viper is a process-global, so these subtests must stay serial.
func TestLokiParams_applyDefaults(t *testing.T) {
	tests := []struct {
		name      string
		viperVals map[string]string
		in        LokiParams
		want      LokiParams
	}{
		{
			name:      "empty params take every viper value",
			viperVals: map[string]string{"url": "http://loki:3100", "username": "u", "password": "p", "token": "tok", "org-id": "org"},
			in:        LokiParams{},
			want: LokiParams{
				URL:      "http://loki:3100",
				Username: "u",
				Password: "p",
				Token:    "tok",
				Org:      "org",
				Format:   "raw",
			},
		},
		{
			name:      "explicit params win over viper",
			viperVals: map[string]string{"url": "http://loki:3100", "username": "u", "password": "p", "token": "tok", "org-id": "org"},
			in: LokiParams{
				URL:      "http://explicit:9999",
				Username: "eu",
				Password: "ep",
				Token:    "etok",
				Org:      "eorg",
				Format:   "json",
			},
			want: LokiParams{
				URL:      "http://explicit:9999",
				Username: "eu",
				Password: "ep",
				Token:    "etok",
				Org:      "eorg",
				Format:   "json",
			},
		},
		{
			name:      "no viper values leaves fields empty but defaults format",
			viperVals: nil,
			in:        LokiParams{},
			want:      LokiParams{Format: "raw"},
		},
		{
			name:      "format is only defaulted when empty",
			viperVals: nil,
			in:        LokiParams{Format: "text"},
			want:      LokiParams{Format: "text"},
		},
		{
			name:      "unrelated fields are untouched",
			viperVals: nil,
			in:        LokiParams{Query: `{job="api"}`, Label: "job", Limit: 5, Head: 2, Filter: "err", FilterCaseSensitive: true},
			want:      LokiParams{Query: `{job="api"}`, Label: "job", Limit: 5, Head: 2, Filter: "err", FilterCaseSensitive: true, Format: "raw"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			for k, v := range tt.viperVals {
				viper.Set(k, v)
			}

			got := tt.in
			got.applyDefaults()

			if got != tt.want {
				t.Errorf("applyDefaults() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestNewLokiClient pins the mapping from LokiParams to the SDK client.
func TestNewLokiClient(t *testing.T) {
	t.Parallel()

	params := &LokiParams{
		URL:      "http://loki:3100",
		Username: "u",
		Password: "p",
		Token:    "tok",
		Org:      "org",
	}

	c := newLokiClient(params)

	if c.Address != params.URL {
		t.Errorf("Address = %q, want %q", c.Address, params.URL)
	}
	if c.Username != params.Username {
		t.Errorf("Username = %q, want %q", c.Username, params.Username)
	}
	if c.Password != params.Password {
		t.Errorf("Password = %q, want %q", c.Password, params.Password)
	}
	if c.BearerToken != params.Token {
		t.Errorf("BearerToken = %q, want %q", c.BearerToken, params.Token)
	}
	if c.OrgID != params.Org {
		t.Errorf("OrgID = %q, want %q", c.OrgID, params.Org)
	}
}
