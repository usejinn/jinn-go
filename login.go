package jinn

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"time"
)

// Login is a CLI login a person approves in the console: open URL, check
// that the console shows Code, approve.
type Login struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	URL       string `json:"url"`
	ExpiresAt int64  `json:"expires_at"`
	secret    string
}

// StartLogin begins a login for a machine. The key's secret is made here
// and never sent; Jinn stores only its hash.
func StartLogin(ctx context.Context, baseURL, machine string) (Login, error) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	secret := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(secret))
	c := New("")
	c.BaseURL = baseURL
	var l Login
	err := c.do(ctx, "POST", "/v1/cli-logins", map[string]string{"name": machine, "key_hash": hex.EncodeToString(sum[:])}, &l)
	l.secret = secret
	return l, err
}

// Wait polls the login every two seconds until a person approves it and
// returns the key (jinn_…).
func (l Login) Wait(ctx context.Context, baseURL string) (string, error) {
	c := New("")
	c.BaseURL = baseURL
	for {
		var s struct {
			State   string `json:"state"`
			Account string `json:"account"`
			Key     string `json:"key"`
		}
		if err := c.do(ctx, "GET", "/v1/cli-logins/"+l.ID, nil, &s); err != nil {
			return "", err
		}
		if s.State == "approved" {
			return "jinn_" + strings.TrimPrefix(s.Account, "acc_") + "_" + strings.TrimPrefix(s.Key, "key_") + "_" + l.secret, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
