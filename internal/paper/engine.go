// Package paper runs the strategy end to end WITHOUT sending a transaction.
//
// It is the honest measurement harness: drive it with a stream of ticks (a real
// tape, or the synthetic feed) and it makes the exact same enter/hedge/merge
// decisions the live bot would, books the exact same P&L the vault arithmetic
// guarantees, and records every round -- locks AND naked expiries together, so
// the account cannot flatter itself by remembering only the wins.
//
// Nothing here can move funds. The point is to earn conviction before risking a
// cent: if the paper lock rate does not clear its fair-market ceiling over a real
// sample, there is no edge and no transaction should ever be built.
//
// One position at a time per round; multiple rounds may be live at once. Sizing
// is a fixed paper stake -- position sizing (Kelly/bankroll) is a separate layer
// applied once an edge is demonstrated, not a thing to co-develop with the proof
// that the edge exists.
package paper

import (
	"fmt"

	"github.com/gauryvg98/prediction-bot/internal/lock"
	"github.com/gauryvg98/prediction-bot/internal/market"
	"github.com/gauryvg98/prediction-bot/internal/signal"
)

// Phase is where a round's position is in its lifecycle.
type Phase uint8

const (
	Flat    Phase = iota // no position; hunting an entry
	OneLeg               // entered; waiting for a hedge
	Locked               // both legs on; guaranteed profit (pre-merge)
	Merged               // redeemed to CASH now via prediCt merge -- terminal, banked
	Naked                // deadline passed unhedged; carrying directional risk to expiry
	Settled              // resolved while unhedged -- terminal, win or loss booked
)

func (p Phase) String() string {
	return [...]string{"FLAT", "ONE_LEG", "LOCKED", "MERGED", "NAKED", "SETTLED"}[p]
}

// Tick is one observation of a round at one instant -- everything a decision
// needs, supplied by the caller so the engine stays pure and replayable.
type Tick struct {
	RoundID         string
	Book            *market.Book
	Spot            float64
	Strike          float64
	SecondsToExpiry float64
	RealizedVol     float64 // per sqrt-second
}

// Resolution is a round's final outcome, for settling any leg still unhedged.
type Resolution struct {
	RoundID     string
	Winner      market.Outcome
	SettlePrice float64
}

// EventKind labels a line in the paper trade log.
type EventKind uint8

const (
	Enter EventKind = iota
	Hedge
	Merge
	Abandon
	Settle
)

func (e EventKind) String() string {
	return [...]string{"ENTER", "HEDGE", "MERGE", "ABANDON", "SETTLE"}[e]
}

// Event is one recorded action, with enough context to audit the decision later.
type Event struct {
	Kind    EventKind
	RoundID string
	Side    market.Outcome
	Price   float64
	Qty     float64 // payout tokens on the leg
	PnL     float64 // realized P&L booked by THIS event (0 for Enter)
	Note    string
}

func (e Event) String() string {
	s := fmt.Sprintf("%-7s %-14s %-4s @ %.4f", e.Kind, trunc(e.RoundID, 14), e.Side, e.Price)
	if e.Kind != Enter {
		s += fmt.Sprintf("  pnl=%+.4f", e.PnL)
	}
	if e.Note != "" {
		s += "  " + e.Note
	}
	return s
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// roundState is one round's live position and how it got there.
type roundState struct {
	phase   Phase
	pos     lock.Position
	entry   *lock.Leg
	barrier float64 // BTC level the entry leg must reach for a hedge (diagnostics)
}

// Config parameterizes a paper run.
type Config struct {
	Fees          market.Fees
	Signal        signal.Params
	Stake         float64 // paper CASH committed on the entry leg
	HedgeDeadline float64 // seconds-to-expiry at/under which we stop waiting and go naked
	RawEntry      bool    // book-only entry (no vol gate) -- for tape replay without per-asset spot
	FairEntry     bool    // fair-edge entry: maker price vs Chainlink-true probability
	Fair          signal.FairParams
}

// DefaultConfig mirrors the strict signal defaults and a $10 paper stake.
func DefaultConfig() Config {
	return Config{
		Fees:          market.Fees{StakeRate: 0.025},
		Signal:        signal.DefaultParams(),
		Fair:          signal.DefaultFairParams(),
		Stake:         10,
		HedgeDeadline: 20,
	}
}

// Engine is the paper trader. Not safe for concurrent use -- drive it from one
// goroutine (the feed loop) and read the Ledger after.
type Engine struct {
	cfg    Config
	rounds map[string]*roundState
	Ledger *Ledger
}

func New(cfg Config) *Engine {
	return &Engine{cfg: cfg, rounds: map[string]*roundState{}, Ledger: NewLedger()}
}

// OnTick advances one round by one observation, returning any events it produced
// (usually none -- the default is to wait). The caller logs/streams these.
func (e *Engine) OnTick(t Tick) []Event {
	st := e.rounds[t.RoundID]
	if st == nil {
		st = &roundState{phase: Flat}
		e.rounds[t.RoundID] = st
	}
	switch st.phase {
	case Flat:
		return e.tryEnter(st, t)
	case OneLeg:
		return e.tryHedge(st, t)
	default:
		return nil // terminal or already resolved
	}
}

func (e *Engine) tryEnter(st *roundState, t Tick) []Event {
	if e.cfg.FairEntry {
		return e.tryEnterFair(st, t)
	}
	if e.cfg.RawEntry {
		return e.tryEnterRaw(st, t)
	}
	in := signal.Inputs{
		Spot: t.Spot, Strike: t.Strike, SecondsToExpiry: t.SecondsToExpiry,
		Book: t.Book, RealizedVol: t.RealizedVol, Fees: e.cfg.Fees,
	}
	d := signal.Evaluate(in, e.cfg.Signal)
	e.Ledger.observe(d) // record the skip/enter decision for gate diagnostics
	if !d.Enter {
		return nil
	}
	price, ok := t.Book.Best(d.Side)
	if !ok {
		return nil
	}
	// Fixed CASH stake buys stake/price payout tokens.
	payout := e.cfg.Stake / price
	leg := &lock.Leg{Outcome: d.Side, Cost: e.cfg.Stake, Payout: payout}
	st.entry = leg
	st.barrier = d.BarrierSpot
	if d.Side == market.Up {
		st.pos.Up = leg
	} else {
		st.pos.Down = leg
	}
	st.phase = OneLeg
	ev := Event{Kind: Enter, RoundID: t.RoundID, Side: d.Side, Price: price, Qty: payout,
		Note: d.Reason}
	e.Ledger.record(ev)
	return []Event{ev}
}

// tryEnterFair is the momentum/mispricing entry: buy the side the maker is
// pricing below its true (Chainlink-implied) probability. Fills are assumed at
// the maker's quoted price.
func (e *Engine) tryEnterFair(st *roundState, t Tick) []Event {
	up, uok := t.Book.Best(market.Up)
	dn, dok := t.Book.Best(market.Down)
	if !uok || !dok {
		return nil
	}
	d := signal.EvaluateFair(t.Spot, t.Strike, t.SecondsToExpiry, t.RealizedVol, up, dn, e.cfg.Fair)
	e.Ledger.observeFair(d)
	if !d.Enter {
		return nil
	}
	price := up
	if d.Side == market.Down {
		price = dn
	}
	if lock.RequiredHedgePrice(price, e.cfg.Fees, e.cfg.Signal.MinLockEdge) <= 0 {
		return nil
	}
	payout := e.cfg.Stake / price
	leg := &lock.Leg{Outcome: d.Side, Cost: e.cfg.Stake, Payout: payout}
	st.entry = leg
	if d.Side == market.Up {
		st.pos.Up = leg
	} else {
		st.pos.Down = leg
	}
	st.phase = OneLeg
	ev := Event{Kind: Enter, RoundID: t.RoundID, Side: d.Side, Price: price, Qty: payout, Note: d.Reason}
	e.Ledger.record(ev)
	return []Event{ev}
}

// tryEnterRaw is the book-only entry used for tape replay: buy the cheaper side
// when it sits in the entry band, there is time left, and a hedge could lock.
// No volatility gate -- that needs per-asset spot the raw tape does not carry.
// This measures the pure LOCK mechanic on real prices: given an entry, how often
// does a hedge actually become available?
func (e *Engine) tryEnterRaw(st *roundState, t Tick) []Event {
	e.Ledger.Evaluated++
	if t.SecondsToExpiry < e.cfg.Signal.MinSecondsLeft {
		return nil
	}
	up, uok := t.Book.Best(market.Up)
	dn, dok := t.Book.Best(market.Down)
	if !uok || !dok {
		return nil
	}
	side, price := market.Up, up
	if dn < up {
		side, price = market.Down, dn
	}
	if price < e.cfg.Signal.MinEntryPrice || price > e.cfg.Signal.MaxEntryPrice {
		return nil
	}
	if lock.RequiredHedgePrice(price, e.cfg.Fees, e.cfg.Signal.MinLockEdge) <= 0 {
		return nil
	}
	e.Ledger.Entered++
	payout := e.cfg.Stake / price
	leg := &lock.Leg{Outcome: side, Cost: e.cfg.Stake, Payout: payout}
	st.entry = leg
	if side == market.Up {
		st.pos.Up = leg
	} else {
		st.pos.Down = leg
	}
	st.phase = OneLeg
	ev := Event{Kind: Enter, RoundID: t.RoundID, Side: side, Price: price, Qty: payout,
		Note: fmt.Sprintf("raw entry: cheaper side @ %.4f", price)}
	e.Ledger.record(ev)
	return []Event{ev}
}

func (e *Engine) tryHedge(st *roundState, t Tick) []Event {
	other := st.entry.Outcome.Other()
	offer, ok := t.Book.Best(other)
	required := lock.RequiredHedgePrice(st.entry.Price(), e.cfg.Fees, e.cfg.Signal.MinLockEdge)

	if ok && required > 0 && offer <= required {
		// Equalize payouts: buy exactly entry.Payout worth on the other side.
		// This maximizes the guaranteed floor and leaves zero residual.
		hedgePayout := st.entry.Payout
		hedgeCost := lock.EqualizingHedgeCost(st.entry.Payout, offer)
		hedge := &lock.Leg{Outcome: other, Cost: hedgeCost, Payout: hedgePayout}
		if other == market.Up {
			st.pos.Up = hedge
		} else {
			st.pos.Down = hedge
		}
		st.phase = Locked
		hev := Event{Kind: Hedge, RoundID: t.RoundID, Side: other, Price: offer,
			Qty: hedgePayout, PnL: 0, Note: fmt.Sprintf("floor=%+.4f", st.pos.Floor())}
		e.Ledger.record(hev)

		// Merge immediately: redeem the matched pair to CASH now, banking the
		// floor and freeing the bankroll -- and cutting exposure to the operator
		// key to near zero. Lock and de-risk are the same action.
		realized := st.pos.RealizedOnMerge()
		st.phase = Merged
		mev := Event{Kind: Merge, RoundID: t.RoundID, Side: other, Price: offer,
			Qty: st.pos.CompleteSets(), PnL: realized,
			Note: fmt.Sprintf("residual=%.4f", st.pos.Residual())}
		e.Ledger.record(mev)
		return []Event{hev, mev}
	}

	if t.SecondsToExpiry <= e.cfg.HedgeDeadline {
		st.phase = Naked
		av := Event{Kind: Abandon, RoundID: t.RoundID, Side: st.entry.Outcome,
			Price: st.entry.Price(), Qty: st.entry.Payout,
			Note: fmt.Sprintf("no hedge; needed <= %.4f", required)}
		e.Ledger.record(av)
		return []Event{av}
	}
	return nil
}

// OnResolve settles any round still carrying an unhedged leg. Locked/merged
// rounds are already terminal and ignore this.
func (e *Engine) OnResolve(r Resolution) []Event {
	st := e.rounds[r.RoundID]
	if st == nil || (st.phase != OneLeg && st.phase != Naked) {
		return nil
	}
	won := st.entry.Outcome == r.Winner
	var pnl float64
	if won {
		pnl = st.entry.Payout*(1-e.cfg.Fees.PayoutRate) - st.entry.Cost
	} else {
		pnl = -st.entry.Cost
	}
	st.phase = Settled
	ev := Event{Kind: Settle, RoundID: r.RoundID, Side: st.entry.Outcome,
		Price: st.entry.Price(), Qty: st.entry.Payout, PnL: pnl,
		Note: fmt.Sprintf("winner=%s %s", r.Winner, wonWord(won))}
	e.Ledger.record(ev)
	return []Event{ev}
}

func wonWord(won bool) string {
	if won {
		return "(naked WIN)"
	}
	return "(naked LOSS)"
}
