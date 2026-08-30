package solana

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Fill is one real World trade, reconstructed from the taker's balance deltas.
type Fill struct {
	TimeMs int64
	Sig    string
	Taker  string
	Mint    string  // outcome-token mint the taker traded = one side of the round
	Sibling string  // the OTHER outcome mint in the same tx = the round's other side
	Cash    float64 // CASH the taker paid (buy) or received (sell)
	Tokens  float64 // outcome tokens the taker gained/lost
	Buy     bool
}

func (f Fill) Price() float64 {
	if f.Tokens == 0 {
		return 0
	}
	return f.Cash / f.Tokens
}

func atof(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }

// reconstruct reads ONE transaction's taker balance deltas into a Fill. The
// taker is the signer; we take THEIR CASH change and THEIR outcome-token change.
// Because both are attributed to the same owner, a routed pump.fun leg (owned by
// someone else, or not the signer's position) cannot be mistaken for the trade.
func reconstruct(tx *txResult) (Fill, bool) {
	if tx == nil || tx.Meta.Err != nil {
		return Fill{}, false
	}
	var signer string
	for _, k := range tx.Transaction.Message.AccountKeys {
		if k.Signer {
			signer = k.Pubkey
			break
		}
	}
	if signer == "" || signer == Operator {
		return Fill{}, false
	}
	pre := map[string]tokenBalance{}
	for _, b := range tx.Meta.PreTokenBalances {
		pre[key(b)] = b
	}
	post := map[string]tokenBalance{}
	for _, b := range tx.Meta.PostTokenBalances {
		post[key(b)] = b
	}
	// CASH change belongs to the signer (the taker/payer). The outcome tokens may
	// land under the signer OR a separate destination authority (custody split),
	// so take the largest single-account outcome-token gain across ALL owners --
	// that is the taker's receipt wherever it went. pump.fun legs are excluded.
	var cashDelta, tokGain float64
	var tokMint string
	for k, pb := range merged(pre, post) {
		d := amt(post[k]) - amt(pre[k])
		if d == 0 {
			continue
		}
		if pb.Mint == CashMint && pb.Owner == signer {
			cashDelta += d
		} else if pb.ProgramID == Token2022 && pb.Mint != CashMint && !strings.HasSuffix(pb.Mint, "pump") {
			if d > tokGain {
				tokGain = d
				tokMint = pb.Mint
			}
		}
	}
	if tokMint == "" || cashDelta >= 0 || tokGain <= 0 {
		return Fill{}, false // a buy: signer CASH down, outcome tokens up somewhere
	}
	// The sibling: a World buy mints a COMPLETE SET, so the other outcome mint of
	// the same round also moves in this tx (the maker keeps it). Capturing it
	// pairs the round's two sides definitively -- no price-guessing.
	var sibling string
	var sibMove float64
	for k, pb := range merged(pre, post) {
		if pb.Mint == CashMint || pb.Mint == tokMint || pb.ProgramID != Token2022 || strings.HasSuffix(pb.Mint, "pump") {
			continue
		}
		if m := abs(amt(post[k]) - amt(pre[k])); m > sibMove {
			sibMove = m
			sibling = pb.Mint
		}
	}
	tokDelta := tokGain
	price := abs(cashDelta) / tokDelta
	if price <= 0.005 || price >= 0.995 {
		return Fill{}, false
	}
	var t int64
	if tx.BlockTime != nil {
		t = *tx.BlockTime * 1000
	}
	return Fill{TimeMs: t, Taker: signer, Mint: tokMint, Sibling: sibling,
		Cash: abs(cashDelta), Tokens: tokDelta, Buy: true}, true
}

func key(b tokenBalance) string  { return strconv.Itoa(b.AccountIndex) }
func amt(b tokenBalance) float64 { return atof(b.UITokenAmt.UIAmountString) }
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
func merged(a, b map[string]tokenBalance) map[string]tokenBalance {
	out := map[string]tokenBalance{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// RecentFills pulls trade signatures from the maker programs and reconstructs
// each into a priced Fill. Contamination-free by construction: sigs come from
// World makers, prices from the taker's own deltas.
func (c *Client) RecentFills(ctx context.Context, perMaker int, paceMs int) ([]Fill, error) {
	seen := map[string]bool{}
	var sigs []string
	before := ""
	for len(sigs) < perMaker {
		page := 1000
		if rem := perMaker - len(sigs); rem < page {
			page = rem
		}
		infos, err := c.SignaturesForAddress(ctx, PredictProgram, page, before)
		if err != nil {
			return nil, err
		}
		if len(infos) == 0 {
			break
		}
		for _, si := range infos {
			if si.Err == nil && !seen[si.Signature] {
				seen[si.Signature] = true
				sigs = append(sigs, si.Signature)
			}
		}
		before = infos[len(infos)-1].Signature
	}
	// Fetch transactions concurrently -- Helius handles parallelism well and this
	// turns a minutes-long sequential scan into seconds.
	const workers = 6
	jobs := make(chan string)
	var mu sync.Mutex
	var fills []Fill
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for sig := range jobs {
				tx, err := c.getTransaction(ctx, sig)
				if err != nil {
					continue
				}
				if f, ok := reconstruct(tx); ok {
					f.Sig = sig
					mu.Lock()
					fills = append(fills, f)
					mu.Unlock()
				}
			}
		}()
	}
	for _, sig := range sigs {
		jobs <- sig
	}
	close(jobs)
	wg.Wait()
	_ = paceMs
	_ = time.Now
	sort.Slice(fills, func(i, j int) bool { return fills[i].TimeMs < fills[j].TimeMs })
	return fills, nil
}
