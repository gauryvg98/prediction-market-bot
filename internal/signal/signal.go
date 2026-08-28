// Package signal decides whether to play a round at all. Its default is NO.
//
// The strategy's whole risk sits in rounds that are entered and never hedged, so
// the highest-leverage control is not the hedge logic -- it is refusing to enter.
// Everything here exists to skip.
//
// # What "the chart will swing" means, precisely
//
// Nothing can know a chart will swing. But the strategy does not need direction:
// buying a cheap leg and hedging once it moves is a LONG VOLATILITY position, so
// the real question is whether the market will travel further than the price
// charges for. That is a comparison, and it is decidable:
//
//  1. Invert the binary quote for the market's IMPLIED volatility (package vol).
//  2. Measure REALIZED volatility from recent spot.
//  3. Convert the lock barrier from price space into a spot level.
//  4. Compute P(touch that level) under REALIZED vol.
//  5. Compare against the fair-market ceiling, entry/barrier.
//
// Step 5 is the discipline. In a fairly-priced market the touch probability
// CANNOT exceed entry_price / barrier_price -- above that, the implied win rate
// among unhedged rounds would have to be negative. So that ratio is the bar, and
// a setup only earns an entry by clearing it. Everything else is a filter around
// this one comparison.
package signal

import (
	"fmt"
	"math"

	"github.com/gauryvg98/prediction-bot/internal/lock"
	"github.com/gauryvg98/prediction-bot/internal/market"
	"github.com/gauryvg98/prediction-bot/internal/vol"
)

// Params are the gates. Defaults are deliberately strict: skipping a good round
// costs one opportunity, entering a bad one costs the stake.
type Params struct {
	MinVolRatio    float64 // realized/implied must clear this. 1.0 = no edge.
	MinTouchEdge   float64 // touch prob must beat the fair ceiling by this margin
	MinEntryPrice  float64 // below this, the move needed is implausible
	MaxEntryPrice  float64 // above this, too little room for a hedge to lock
	MinSecondsLeft float64 // the move needs time; late entries cannot travel
	MinLockEdge    float64 // profit cushion demanded of any hedge, per $1 payout
}

// DefaultParams is a starting point, not a tuned configuration. Every number
// here should be replaced by one the measurement phase justifies.
func DefaultParams() Params {
	return Params{
		MinVolRatio:    1.25,
		MinTouchEdge:   0.05,
		MinEntryPrice:  0.10,
		MaxEntryPrice:  0.45,
		MinSecondsLeft: 120,
		MinLockEdge:    0.02,
	}
}

// Inputs is everything one decision needs. No clock, no I/O: the caller supplies
// the world, so a decision is reproducible against a recorded tape.
type Inputs struct {
	Spot            float64 // current BTC spot
	Strike          float64 // the round's target price
	SecondsToExpiry float64
	Book            *market.Book
	RealizedVol     float64 // per sqrt-second, from package vol
	Fees            market.Fees
}

// Decision is the answer plus every number behind it. The diagnostics are not
// decoration: a skip that cannot explain itself cannot be tuned, and the
// per-round record is what the measurement phase consumes.
type Decision struct {
	Enter  bool
	Side   market.Outcome
	Reason string

	EntryPrice   float64
	ImpliedVol   float64
	RealizedVol  float64
	VolRatio     float64
	BarrierPrice float64 // in binary-price space
	BarrierSpot  float64 // the same barrier as a BTC level
	TouchProb    float64 // P(reach the barrier) under REALIZED vol
	FairCeiling  float64 // entry/barrier -- the bar a fair market cannot beat
	Edge         float64 // TouchProb - FairCeiling
	RequiredHedg float64 // most we may pay for the other side

	// stage records how far this side got before being rejected. When both
	// sides skip, the one that progressed furthest has the most useful
	// explanation -- "UP was cheap enough but vol was not underpriced" tells
	// you something; "DOWN was too expensive" is noise you already knew.
	stage int
}

// Stages a side passes through, in order. Higher means it got further.
const (
	stageUnquoted = iota
	stagePriceBand
	stageImpliedVol
	stageVolRatio
	stageHedgeable
	stageTouchEdge
)

func skipAt(stage int, format string, a ...any) Decision {
	return Decision{Enter: false, stage: stage, Reason: fmt.Sprintf(format, a...)}
}

func skip(format string, a ...any) Decision {
	return skipAt(stageUnquoted, format, a...)
}

// spotForUpPrice converts a probability-of-UP into the BTC level implying it.
// Inverse of the short-horizon binary relation p = N(ln(S/K)/(sigma*sqrt(T))).
func spotForUpPrice(strike, pUp, sigma, seconds float64) float64 {
	return strike * math.Exp(vol.NormInv(pUp)*sigma*math.Sqrt(seconds))
}

// Evaluate decides one round. It considers BOTH sides and takes the better, since
// the cheap leg is whichever one the market has moved away from.
func Evaluate(in Inputs, p Params) Decision {
	if in.Book == nil {
		return skip("no book")
	}
	if in.SecondsToExpiry < p.MinSecondsLeft {
		return skip("too-late: %.0fs left, need %.0fs", in.SecondsToExpiry, p.MinSecondsLeft)
	}
	if in.RealizedVol <= 0 {
		return skip("no realized vol")
	}
	over, ok := in.Book.Overround()
	if !ok {
		return skip("book incomplete")
	}

	best := skip("no side qualified")
	for _, side := range []market.Outcome{market.Up, market.Down} {
		d := evaluateSide(in, p, side, over)
		switch {
		case d.Enter && (!best.Enter || d.Edge > best.Edge):
			best = d // among entries, take the bigger edge
		case !best.Enter && !d.Enter && d.stage > best.stage:
			best = d // among skips, keep the one that got furthest
		}
	}
	return best
}

func evaluateSide(in Inputs, p Params, side market.Outcome, over float64) Decision {
	entry, ok := in.Book.Best(side)
	if !ok {
		return skipAt(stageUnquoted, "%s unquoted", side)
	}
	if entry < p.MinEntryPrice || entry > p.MaxEntryPrice {
		return skipAt(stagePriceBand, "%s entry %.3f outside band [%.2f,%.2f]",
			side, entry, p.MinEntryPrice, p.MaxEntryPrice)
	}

	// Implied vol is inverted from the UP leg's price by convention, so express
	// this side's quote as a probability of UP first.
	pUp := entry
	if side == market.Down {
		pUp = 1 - entry
	}
	iv, err := vol.Implied(in.Spot, in.Strike, in.SecondsToExpiry, pUp)
	if err != nil {
		return skipAt(stageImpliedVol, "%s implied vol: %v", side, err)
	}

	ratio := in.RealizedVol / iv
	if ratio < p.MinVolRatio {
		return skipAt(stageVolRatio,
			"%s vol ratio %.2f < %.2f (market is not underpricing movement)",
			side, ratio, p.MinVolRatio)
	}

	barrier := lock.BarrierPrice(entry, over, in.Fees, p.MinLockEdge)
	if barrier >= 1 {
		return skipAt(stageHedgeable, "%s unhedgeable: needs price >= %.3f", side, barrier)
	}
	required := lock.RequiredHedgePrice(entry, in.Fees, p.MinLockEdge)
	if required <= 0 {
		return skipAt(stageHedgeable, "%s entry too rich to rescue", side)
	}

	// The barrier lives in price space; the touch test lives in spot space.
	// Convert using IMPLIED vol, because that is what the market will reprice
	// with -- then judge reachability with REALIZED vol, which is what moves it.
	barrierUp := barrier
	if side == market.Down {
		barrierUp = 1 - barrier
	}
	barrierSpot := spotForUpPrice(in.Strike, barrierUp, iv, in.SecondsToExpiry)
	touch := vol.TouchProbability(in.Spot, barrierSpot, in.SecondsToExpiry, in.RealizedVol)

	ceiling := entry / barrier // what a fair market caps the lock rate at
	edge := touch - ceiling

	d := Decision{
		Side: side, EntryPrice: entry, ImpliedVol: iv, RealizedVol: in.RealizedVol,
		stage:    stageTouchEdge,
		VolRatio: ratio, BarrierPrice: barrier, BarrierSpot: barrierSpot,
		TouchProb: touch, FairCeiling: ceiling, Edge: edge, RequiredHedg: required,
	}
	if edge < p.MinTouchEdge {
		d.Reason = fmt.Sprintf("%s touch %.1f%% vs fair ceiling %.1f%% -- edge %.1f%% below %.1f%%",
			side, touch*100, ceiling*100, edge*100, p.MinTouchEdge*100)
		return d
	}
	d.Enter = true
	d.Reason = fmt.Sprintf("%s: realized vol %.2fx implied, touch %.1f%% vs ceiling %.1f%%",
		side, ratio, touch*100, ceiling*100)
	return d
}
