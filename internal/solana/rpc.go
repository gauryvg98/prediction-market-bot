// Package solana is a minimal JSON-RPC client for reading the World tape straight
// from a Solana node (Helius in production, public mainnet for validation).
//
// Reading from chain -- rather than an indexer -- removes the quota wall and,
// crucially, removes contamination: we take trade signatures from a World
// program and reconstruct each trade from the taker's OWN pre/post balance
// deltas, so a pump.fun token bought with CASH can never masquerade as an
// outcome. The endpoint is the only thing that changes between validation and
// production.
package solana

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	PredictProgram = "prediCtPZCttYMvm2W3PtxmMxLmT1dtN7riU6Cxh6tM"
	CashMint       = "CASHx9KJUStyftLFWGvEVf59SGeG9sh5FfcnZMVPCASH"
	Token2022      = "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb"
	JanusMaker     = "JanusXpm3gsW3c9ErNoUgHppL8dGLvZKB7uekkJEYFP"
	BisonMaker     = "2DNbzPochEcyCcWMbL4d9S3u9QqQEj5bbe6cSZFvKsbh"
	Operator       = "DDucv2DeUsTsg1rfAcWAnUSUVpqfdHEzxX66ARB2JYVg"
)

type Client struct {
	url      string
	http     *http.Client
	cacheDir string     // getTransaction cache: a tx is immutable, so never re-fetch
	limiter  <-chan time.Time // token bucket: stay under the free-tier rate limit
}

func New(url string) *Client {
	c := &Client{url: url, http: &http.Client{Timeout: 30 * time.Second}, cacheDir: "data/txcache",
		limiter: time.Tick(130 * time.Millisecond)} // ~7.7 req/s, safely under free-tier 10
	_ = os.MkdirAll(c.cacheDir, 0o755)
	return c
}

func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	if c.limiter != nil {
		select {
		case <-c.limiter:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": params,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("rpc http %d: %.120s", resp.StatusCode, raw)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return err
	}
	if env.Error != nil {
		return fmt.Errorf("rpc error %d: %s", env.Error.Code, env.Error.Message)
	}
	return json.Unmarshal(env.Result, out)
}

// SignatureInfo is one entry from getSignaturesForAddress.
type SignatureInfo struct {
	Signature string `json:"signature"`
	Slot      int64  `json:"slot"`
	BlockTime *int64 `json:"blockTime"`
	Err       any    `json:"err"`
}

// SignaturesForAddress returns recent signatures that mention addr (including as
// a program). Used on a maker program to get trade signatures only.
func (c *Client) SignaturesForAddress(ctx context.Context, addr string, limit int, before string) ([]SignatureInfo, error) {
	opts := map[string]any{"limit": limit, "commitment": "confirmed"}
	if before != "" {
		opts["before"] = before
	}
	var out []SignatureInfo
	err := c.call(ctx, "getSignaturesForAddress", []any{addr, opts}, &out)
	return out, err
}

// --- getTransaction, reduced to the balance deltas we need ------------------

type tokenBalance struct {
	AccountIndex int    `json:"accountIndex"`
	Mint         string `json:"mint"`
	Owner        string `json:"owner"`
	ProgramID    string `json:"programId"`
	UITokenAmt   struct {
		UIAmountString string `json:"uiAmountString"`
	} `json:"uiTokenAmount"`
}

type txResult struct {
	BlockTime   *int64 `json:"blockTime"`
	Transaction struct {
		Message struct {
			AccountKeys []struct {
				Pubkey string `json:"pubkey"`
				Signer bool   `json:"signer"`
			} `json:"accountKeys"`
		} `json:"message"`
	} `json:"transaction"`
	Meta struct {
		Err               any            `json:"err"`
		PreTokenBalances  []tokenBalance `json:"preTokenBalances"`
		PostTokenBalances []tokenBalance `json:"postTokenBalances"`
	} `json:"meta"`
}

// GetTransaction fetches one parsed transaction, caching it to disk. A confirmed
// transaction is immutable, so a cache hit costs no RPC credits -- essential when
// scanning thousands of sigs against a metered endpoint.
func (c *Client) getTransaction(ctx context.Context, sig string) (*txResult, error) {
	path := filepath.Join(c.cacheDir, sig+".json")
	if b, err := os.ReadFile(path); err == nil {
		var out txResult
		if json.Unmarshal(b, &out) == nil {
			return &out, nil
		}
	}
	opts := map[string]any{"encoding": "jsonParsed", "maxSupportedTransactionVersion": 0, "commitment": "confirmed"}
	var raw json.RawMessage
	if err := c.call(ctx, "getTransaction", []any{sig, opts}, &raw); err != nil {
		return nil, err
	}
	_ = os.WriteFile(path, raw, 0o644)
	var out txResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
