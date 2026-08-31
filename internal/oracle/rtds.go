// Package oracle consumes BTC prices over Polymarket's RTDS WebSocket -- a
// no-credentials relay of the SAME Chainlink Data Streams TWAP that settles
// short crypto prediction markets, plus Binance spot on the same socket.
//
// Two prices, one connection:
//   - Chainlink btc/usd  -> the RESOLUTION oracle. Matching it means our
//     distance-to-strike agrees with what actually settles the round (World
//     resolves on Chainlink Data Streams; this is that feed).
//   - Binance btcusdt     -> fast spot for the volatility estimate.
//
// wss://ws-live-data.polymarket.com. Topic-based subscribe; PING every 5s.
package oracle

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const Endpoint = "wss://ws-live-data.polymarket.com"

// Tick is one price update from a source.
type Tick struct {
	Source string // "chainlink" or "binance"
	Symbol string
	Value  float64
	TimeMs int64
}

// Client streams BTC prices and keeps the latest of each source.
type Client struct {
	mu        sync.RWMutex
	chainlink float64
	binance   float64
	clTimeMs  int64
	bnTimeMs  int64
	onTick    func(Tick)
}

func New() *Client { return &Client{} }

// OnTick registers a callback fired on every price update (optional).
func (c *Client) OnTick(f func(Tick)) { c.onTick = f }

// Chainlink returns the latest Chainlink btc/usd (the resolution oracle).
func (c *Client) Chainlink() (float64, int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.chainlink, c.clTimeMs
}

// Binance returns the latest Binance btcusdt (fast spot).
func (c *Client) Binance() (float64, int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.binance, c.bnTimeMs
}

type subMsg struct {
	Action        string   `json:"action"`
	Subscriptions []subTop `json:"subscriptions"`
}
type subTop struct {
	Topic   string `json:"topic"`
	Type    string `json:"type"`
	Filters string `json:"filters"`
}

// Run connects, subscribes to Chainlink + Binance BTC, and pumps ticks until
// ctx is cancelled. Reconnects are the caller's job (wrap in a loop).
func (c *Client) Run(ctx context.Context) error {
	conn, _, err := websocket.Dial(ctx, Endpoint, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(1 << 20)

	sub := subMsg{Action: "subscribe", Subscriptions: []subTop{
		{Topic: "crypto_prices_chainlink", Type: "update", Filters: `{"symbol":"btc/usd"}`}, // resolution oracle
		{Topic: "crypto_prices", Type: "update", Filters: `{"symbol":"btcusdt"}`},           // fast spot
	}}
	b, _ := json.Marshal(sub)
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}

	// keepalive: PING every 5s
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = conn.Write(ctx, websocket.MessageText, []byte("ping"))
			}
		}
	}()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		c.handle(data)
	}
}

// handle parses an incoming message. The exact schema is confirmed against the
// live socket in the smoke test; this tolerates the documented shapes.
func (c *Client) handle(data []byte) {
	var m struct {
		Topic   string `json:"topic"`
		Type    string `json:"type"`
		Payload struct {
			Symbol    string `json:"symbol"`
			Value     any    `json:"value"`
			Timestamp int64  `json:"timestamp"`
			Data      []struct {
				Timestamp int64 `json:"timestamp"`
				Value     any   `json:"value"`
			} `json:"data"`
		} `json:"payload"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	val := toFloat(m.Payload.Value)
	ts := m.Payload.Timestamp
	sym := m.Payload.Symbol
	if len(m.Payload.Data) > 0 { // historical dump (type "subscribe"): latest point
		last := m.Payload.Data[len(m.Payload.Data)-1]
		val = toFloat(last.Value)
		ts = last.Timestamp
	}
	if val == 0 {
		return
	}
	if ts == 0 {
		ts = time.Now().UnixMilli()
	}
	// Chainlink rides topic crypto_prices_chainlink / symbol btc/usd; Binance
	// rides crypto_prices / btcusdt. Route by whichever is present.
	src := "binance"
	c.mu.Lock()
	if contains(m.Topic, "chainlink") || contains(sym, "/") {
		src = "chainlink"
		c.chainlink, c.clTimeMs = val, ts
	} else {
		c.binance, c.bnTimeMs = val, ts
	}
	c.mu.Unlock()
	if c.onTick != nil {
		c.onTick(Tick{Source: src, Symbol: sym, Value: val, TimeMs: ts})
	}
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		var f float64
		fmt.Sscanf(x, "%g", &f)
		return f
	}
	return 0
}
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
