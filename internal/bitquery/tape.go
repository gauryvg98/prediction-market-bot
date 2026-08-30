package bitquery

import (
	"context"
	"fmt"
	"sort"
	"strconv"
)

// Bitquery returns numeric Amounts as JSON strings; parse defensively.
func atof(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }

// --- raw shapes (Amount as string) ------------------------------------------

type rawTfHdr struct {
	Block       struct{ Time string }
	Transaction struct {
		Signature string
		Signer    string
	}
	Transfer struct {
		Amount   string
		Currency struct{ MintAddress, ProgramAddress string }
		Sender   struct{ Address string }
		Receiver struct{ Address string }
	}
}

func (r rawTfHdr) toTransfer() Transfer {
	return Transfer{
		TimeISO: r.Block.Time, Signature: r.Transaction.Signature, Signer: r.Transaction.Signer,
		Mint: r.Transfer.Currency.MintAddress, ProgramId: r.Transfer.Currency.ProgramAddress, Amount: atof(r.Transfer.Amount),
		Sender: r.Transfer.Sender.Address, Receiver: r.Transfer.Receiver.Address,
	}
}

// --- CASH legs (proven query): recent World trade signatures ----------------

func (c *Client) cashLegs(ctx context.Context, sinceISO string, limit int) ([]Transfer, error) {
	q := fmt.Sprintf(`{ Solana { Transfers(
		where: { Transfer: { Currency: { MintAddress: { is: "%s" } } }, Block: { Time: { since: "%s" } } }
		limit: { count: %d } orderBy: { descending: Block_Time }
	) { Block { Time } Transaction { Signature Signer } Transfer { Amount Currency { MintAddress ProgramAddress } Sender { Address } Receiver { Address } } } } }`,
		CashMint, sinceISO, limit)
	var r struct{ Solana struct{ Transfers []rawTfHdr } }
	if err := c.Query(ctx, q, &r); err != nil {
		return nil, err
	}
	out := make([]Transfer, len(r.Solana.Transfers))
	for i, t := range r.Solana.Transfers {
		out[i] = t.toTransfer()
	}
	return out, nil
}

// TxLegs returns every transfer leg in one transaction -- used to find the
// outcome-token leg paired with a CASH payment.
func (c *Client) TxLegs(ctx context.Context, sig string) ([]Transfer, error) {
	q := fmt.Sprintf(`{ Solana { Transfers(
		where: { Transaction: { Signature: { is: "%s" } } } limit: { count: 40 }
	) { Block { Time } Transaction { Signature Signer } Transfer { Amount Currency { MintAddress ProgramAddress } Sender { Address } Receiver { Address } } } } }`, sig)
	var r struct{ Solana struct{ Transfers []rawTfHdr } }
	if err := c.Query(ctx, q, &r); err != nil {
		return nil, err
	}
	out := make([]Transfer, len(r.Solana.Transfers))
	for i, t := range r.Solana.Transfers {
		out[i] = t.toTransfer()
	}
	return out, nil
}

// Fill is one priced outcome-token trade -- the tape's atom.
type Fill struct {
	TimeISO string
	Sig     string
	Taker   string
	Mint    string  // outcome-token mint = the side
	Cash    float64 // CASH paid (buy) or received (sell)
	Tokens  float64 // outcome tokens moved
	Buy     bool
}

func (f Fill) Price() float64 {
	if f.Tokens == 0 {
		return 0
	}
	return f.Cash / f.Tokens
}

// isOutcomeMint filters out CASH and the USDC ramp leg: an outcome token is a
// non-CASH mint whose price lands strictly inside (0,1) -- a probability. The
// USDC leg prices ~1.0 (a stablecoin hop) and is rejected by that alone.
func priceInBand(p float64) bool { return p > 0.005 && p < 0.995 }

// RecentFills discovers priced outcome-token trades from the live tape: pull
// recent CASH legs, then for each transaction find the outcome leg and price it.
// One extra query per signature -- bounded by `maxTrades`, so callers control cost.
func (c *Client) RecentFills(ctx context.Context, sinceISO string, cashLimit, maxTrades int) ([]Fill, error) {
	legs, err := c.cashLegs(ctx, sinceISO, cashLimit)
	if err != nil {
		return nil, err
	}
	// dedupe signatures, newest first, cap the number we expand
	seen := map[string]bool{}
	var sigs []string
	for _, l := range legs {
		if !seen[l.Signature] {
			seen[l.Signature] = true
			sigs = append(sigs, l.Signature)
		}
	}
	if len(sigs) > maxTrades {
		sigs = sigs[:maxTrades]
	}
	var fills []Fill
	makers := map[string]bool{JanusMaker: true, BisonMaker: true}
	_ = makers
	for _, sig := range sigs {
		tl, err := c.TxLegs(ctx, sig)
		if err != nil {
			continue
		}
		f, ok := priceTx(tl)
		if ok {
			fills = append(fills, f)
		}
	}
	sort.Slice(fills, func(i, j int) bool { return fills[i].TimeISO < fills[j].TimeISO })
	return fills, nil
}

// priceTx reduces one transaction's legs to a single priced outcome trade:
// the CASH total against the best-scoring outcome-token leg (in-band price).
func priceTx(legs []Transfer) (Fill, bool) {
	var cash float64
	var timeISO, sig, taker string
	// aggregate CASH; collect candidate outcome legs by mint
	tokByMint := map[string]float64{}
	recvByMint := map[string]string{}
	sendByMint := map[string]string{}
	for _, l := range legs {
		timeISO, sig = l.TimeISO, l.Signature
		if l.Mint == CashMint {
			if l.Amount > cash {
				cash = l.Amount
			}
			continue
		}
		if l.ProgramId != Token2022 { // outcome tokens are Token-2022; skips SOL/USDC hops
			continue
		}
		if l.Amount > tokByMint[l.Mint] {
			tokByMint[l.Mint] = l.Amount
			recvByMint[l.Mint] = l.Receiver
			sendByMint[l.Mint] = l.Sender
		}
	}
	if cash <= 0 {
		return Fill{}, false
	}
	// pick the mint whose implied price sits inside (0,1): the outcome token.
	bestMint, bestTok, best := "", 0.0, -1.0
	for m, tok := range tokByMint {
		p := cash / tok
		if priceInBand(p) {
			// prefer the price nearest a plausible mid (least extreme) -- the
			// real outcome leg, not a dust/rounding leg.
			score := 1 - absf(p-0.5)
			if score > best {
				best, bestMint, bestTok = score, m, tok
			}
		}
	}
	if bestMint == "" {
		return Fill{}, false
	}
	// taker is the signer; buy if the taker received the outcome tokens
	taker = legsSigner(legs)
	buy := recvByMint[bestMint] == taker
	return Fill{TimeISO: timeISO, Sig: sig, Taker: taker, Mint: bestMint,
		Cash: cash, Tokens: bestTok, Buy: buy}, true
}

func legsSigner(legs []Transfer) string {
	for _, l := range legs {
		if l.Signer != "" {
			return l.Signer
		}
	}
	return ""
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
