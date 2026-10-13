package jinn

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Event types.
const (
	EventRunRunning      = "run.running"
	EventRunMaintenance  = "run.maintenance"
	EventRunSucceeded    = "run.succeeded"
	EventRunFailed       = "run.failed"
	EventCommandFinished = "command.finished"
)

// Event is a webhook's body. A run's events carry the run as Run returns it;
// command.finished carries the command. Sequence is the run's, at the
// change the event tells of.
type Event struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Created  time.Time       `json:"created"`
	Sequence int64           `json:"sequence"`
	Data     json.RawMessage `json:"data"`
}

// Run is a run event's run.
func (e Event) Run() (Run, error) {
	var r Run
	return r, json.Unmarshal(e.Data, &r)
}

// Command is a command.finished event's command.
func (e Event) Command() (Command, error) {
	var c Command
	return c, json.Unmarshal(e.Data, &c)
}

// VerifyWebhook checks a webhook (Standard Webhooks, v1a, Ed25519) with your
// account's public key (whpk_…, in the console and at
// GET /v1/webhooks/public-key) and returns its event. It refuses one signed
// more than five minutes from now.
func VerifyWebhook(publicKey string, h http.Header, body []byte, now time.Time) (Event, error) {
	var e Event
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(publicKey, "whpk_"))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return e, errors.New("jinn: the public key is not a whpk_ key")
	}
	id, ts := h.Get("webhook-id"), h.Get("webhook-timestamp")
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || now.Sub(time.Unix(sec, 0)).Abs() > 5*time.Minute {
		return e, errors.New("jinn: the webhook's timestamp is missing or too far from now")
	}
	signed := []byte(id + "." + ts + "." + string(body))
	for _, s := range strings.Fields(h.Get("webhook-signature")) {
		v, sig, ok := strings.Cut(s, ",")
		if !ok || v != "v1a" {
			continue
		}
		if b, err := base64.StdEncoding.DecodeString(sig); err == nil && ed25519.Verify(raw, signed, b) {
			return e, json.Unmarshal(body, &e)
		}
	}
	return e, errors.New("jinn: no signature matches")
}
