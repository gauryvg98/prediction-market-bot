package bitquery

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
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
	for i, sig := range sigs {
		if i > 0 {
			time.Sleep(2 * time.Second) // pace under Bitquery's per-minute limit
		}
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
		if strings.HasSuffix(l.Mint, "pump") { // never a World market token
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

// --- bulk per-mint tape (rate-limit friendly: one query per mint) -----------

// CashBySig pulls all CASH legs in a window in ONE query and maps signature ->
// max CASH moved. This is the join table for pricing per-mint fills without a
// query per trade.
func (c *Client) CashBySig(ctx context.Context, sinceISO, tillISO string, limit int) (map[string]float64, error) {
	q := fmt.Sprintf(`{ Solana { Transfers(
		where: { Transfer: { Currency: { MintAddress: { is: "%s" } } }, Block: { Time: { since: "%s", till: "%s" } } }
		limit: { count: %d } orderBy: { descending: Block_Time }
	) { Transaction { Signature } Transfer { Amount } } } }`, CashMint, sinceISO, tillISO, limit)
	var r struct {
		Solana struct {
			Transfers []struct {
				Transaction struct{ Signature string }
				Transfer    struct{ Amount string }
			}
		}
	}
	if err := c.Query(ctx, q, &r); err != nil {
		return nil, err
	}
	m := map[string]float64{}
	for _, t := range r.Solana.Transfers {
		a := atof(t.Transfer.Amount)
		if a > m[t.Transaction.Signature] {
			m[t.Transaction.Signature] = a
		}
	}
	return m, nil
}

// MintFills pulls every transfer of one outcome mint in a window (ONE query) and
// prices each against the CASH map. This is the production path: a full per-side
// price series in a single request, no per-signature expansion.
func (c *Client) MintFills(ctx context.Context, mint, sinceISO, tillISO string, limit int, cash map[string]float64) ([]Fill, error) {
	q := fmt.Sprintf(`{ Solana { Transfers(
		where: { Transfer: { Currency: { MintAddress: { is: "%s" } } }, Block: { Time: { since: "%s", till: "%s" } } }
		limit: { count: %d } orderBy: { descending: Block_Time }
	) { Block { Time } Transaction { Signature Signer } Transfer { Amount Sender { Address } Receiver { Address } } } } }`,
		mint, sinceISO, tillISO, limit)
	var r struct{ Solana struct{ Transfers []rawTfHdr } }
	if err := c.Query(ctx, q, &r); err != nil {
		return nil, err
	}
	var out []Fill
	for _, t := range r.Solana.Transfers {
		sig := t.Transaction.Signature
		csh := cash[sig]
		tok := atof(t.Transfer.Amount)
		if csh <= 0 || tok <= 0 {
			continue
		}
		p := csh / tok
		if !priceInBand(p) {
			continue
		}
		out = append(out, Fill{TimeISO: t.Block.Time, Sig: sig, Taker: t.Transaction.Signer,
			Mint: mint, Cash: csh, Tokens: tok, Buy: t.Transfer.Sender.Address != t.Transaction.Signer})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TimeISO < out[j].TimeISO })
	return out, nil
}

// OutcomeTransfers pulls every outcome-token transfer in a window in ONE query:
// Token-2022 program, minus CASH. High-yield discovery of all live sides.
func (c *Client) OutcomeTransfers(ctx context.Context, sinceISO, tillISO string, limit int) ([]Transfer, error) {
	q := fmt.Sprintf(`{ Solana { Transfers(
		where: { Transfer: { Currency: { ProgramAddress: { is: "%s" }, MintAddress: { not: "%s" } } }, Block: { Time: { since: "%s", till: "%s" } } }
		limit: { count: %d } orderBy: { descending: Block_Time }
	) { Block { Time } Transaction { Signature Signer } Transfer { Amount Currency { MintAddress } Sender { Address } Receiver { Address } } } } }`,
		Token2022, CashMint, sinceISO, tillISO, limit)
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

// FillsFromTape joins outcome transfers with a CASH map into priced fills.
func FillsFromTape(outs []Transfer, cash map[string]float64) []Fill {
	var fills []Fill
	for _, t := range outs {
		csh := cash[t.Signature]
		if csh <= 0 || t.Amount <= 0 {
			continue
		}
		p := csh / t.Amount
		if !priceInBand(p) {
			continue
		}
		fills = append(fills, Fill{TimeISO: t.TimeISO, Sig: t.Signature, Taker: t.Signer,
			Mint: t.Mint, Cash: csh, Tokens: t.Amount, Buy: t.Sender != t.Signer})
	}
	sort.Slice(fills, func(i, j int) bool { return fills[i].TimeISO < fills[j].TimeISO })
	return fills
}

// --- batched reconstruction (the production path) ---------------------------
// Trades are a small fraction of CASH movements, so we scan many CASH sigs but
// fetch their legs in BATCHES via a signature `in` filter -- high fidelity and
// few queries. This is what makes live reconstruction viable under rate limits.

// WorldTradeSigs returns signatures of taker-initiated World trades: transactions
// carrying a prediCt instruction whose signer is NOT the operator key. This is
// the fix for contamination -- sourcing from ALL CASH movements also caught
// pump.fun and other Token-2022 tokens bought with CASH. prediCt-involvement
// guarantees the outcome leg is a real World market token.
func (c *Client) WorldTradeSigs(ctx context.Context, sinceISO, tillISO string, limit int) ([]string, error) {
	q := fmt.Sprintf(`{ Solana { Instructions(
		where: { Instruction: { Program: { Address: { is: "%s" } } },
		         Transaction: { Signer: { not: "%s" }, Result: { Success: true } },
		         Block: { Time: { since: "%s", till: "%s" } } }
		limit: { count: %d } orderBy: { descending: Block_Time }
	) { Transaction { Signature } } } }`, PredictProgram, Operator, sinceISO, tillISO, limit)
	var r struct {
		Solana struct {
			Instructions []struct{ Transaction struct{ Signature string } }
		}
	}
	if err := c.Query(ctx, q, &r); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range r.Solana.Instructions {
		s := t.Transaction.Signature
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out, nil
}

func (c *Client) cashSigs(ctx context.Context, sinceISO, tillISO string, limit int) ([]string, error) {
	q := fmt.Sprintf(`{ Solana { Transfers(
		where: { Transfer: { Currency: { MintAddress: { is: "%s" } } }, Block: { Time: { since: "%s", till: "%s" } } }
		limit: { count: %d } orderBy: { descending: Block_Time }
	) { Transaction { Signature } } } }`, CashMint, sinceISO, tillISO, limit)
	var r struct {
		Solana struct {
			Transfers []struct{ Transaction struct{ Signature string } }
		}
	}
	if err := c.Query(ctx, q, &r); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range r.Solana.Transfers {
		s := t.Transaction.Signature
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out, nil
}

func (c *Client) legsBatch(ctx context.Context, sigs []string) (map[string][]Transfer, error) {
	quoted := make([]string, len(sigs))
	for i, s := range sigs {
		quoted[i] = `"` + s + `"`
	}
	inList := "[" + join(quoted, ",") + "]"
	q := fmt.Sprintf(`{ Solana { Transfers(
		where: { Transaction: { Signature: { in: %s } } } limit: { count: %d }
	) { Block { Time } Transaction { Signature Signer } Transfer { Amount Currency { MintAddress ProgramAddress } Sender { Address } Receiver { Address } } } } }`,
		inList, len(sigs)*25)
	var r struct{ Solana struct{ Transfers []rawTfHdr } }
	if err := c.Query(ctx, q, &r); err != nil {
		return nil, err
	}
	bySig := map[string][]Transfer{}
	for _, t := range r.Solana.Transfers {
		tr := t.toTransfer()
		bySig[tr.Signature] = append(bySig[tr.Signature], tr)
	}
	return bySig, nil
}

// RecentFillsBatched pulls up to `cashLimit` recent CASH signatures and prices
// their trades in batches of 50 via the `in` filter. Returns clean priced fills.
func (c *Client) RecentFillsBatched(ctx context.Context, sinceISO, tillISO string, cashLimit int) ([]Fill, error) {
	sigs, err := c.WorldTradeSigs(ctx, sinceISO, tillISO, cashLimit)
	if err != nil {
		return nil, err
	}
	var fills []Fill
	const batch = 50
	for i := 0; i < len(sigs); i += batch {
		j := i + batch
		if j > len(sigs) {
			j = len(sigs)
		}
		legs, err := c.legsBatch(ctx, sigs[i:j])
		if err != nil {
			continue
		}
		for _, ls := range legs {
			if f, ok := priceTx(ls); ok {
				fills = append(fills, f)
			}
		}
		time.Sleep(1500 * time.Millisecond)
	}
	sort.Slice(fills, func(i, j int) bool { return fills[i].TimeISO < fills[j].TimeISO })
	return fills, nil
}

func join(a []string, sep string) string {
	if len(a) == 0 {
		return ""
	}
	out := a[0]
	for _, s := range a[1:] {
		out += sep + s
	}
	return out
}
