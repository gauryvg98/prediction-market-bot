// Package lock is the two-leg engine: what a hedge must cost, and what a
// position is actually worth.
//
// The two legs are bought at DIFFERENT times, which makes this legging, not
// arbitrage. The consequence is a single identity worth internalizing. At any
// instant the venue quotes p_up + p_down = 1 + v with v > 0. Holding one side
// bought at p1, the hedge costs (1+v) - p_entry_now, so a lock needs:
//
//	p_entry_now  >  p1 + v + (1 - breakeven)
//
// The leg already held must have RISEN before a profitable hedge is offered --
// the hedge appears only once the first leg is winning. That is why entry
// selection (package signal) does all the work, and why a losing round cannot
// simply be hedged out: the escape is quoted further away the more it is needed.
//
// What IS guaranteed: once both legs are on with S < breakeven, the profit is
// certain, because World mints and burns complete sets at par. That guarantee is
// structural, not statistical.
package lock

import (
	"math"

	"github.com/gauryvg98/prediction-bot/internal/market"
)

// RequiredHedgePrice is the most we may pay for the other side and still lock.
// A non-positive result means the entry is already too expensive to rescue.
func RequiredHedgePrice(entryPrice float64, f market.Fees, minEdge float64) float64 {
	return f.BreakevenSum() - entryPrice - minEdge
}

// BarrierPrice is the price our ENTRY leg must reach before any hedge locks.
// Depends on no view, signal, or level -- only on friction.
func BarrierPrice(entryPrice, overround float64, f market.Fees, minEdge float64) float64 {
	return entryPrice + overround + (1 - f.BreakevenSum()) + minEdge
}

// LockPnL is the realized P&L of a completed lock, per `qty` of payout on each
// side. Identical whichever way the round resolves -- equal size is what makes
// the payout outcome-independent.
func LockPnL(qty, entryPrice, hedgePrice float64, f market.Fees) float64 {
	payout := qty * (1 - f.PayoutRate)
	cost := qty * (entryPrice + hedgePrice) * (1 + f.StakeRate)
	return payout - cost
}

// Leg is one filled leg: what it cost, and what it pays if its side wins.
type Leg struct {
	Outcome market.Outcome
	Cost    float64
	Payout  float64
}

// Price is cost per $1 of payout -- the implied probability paid.
func (l Leg) Price() float64 {
	if l.Payout == 0 {
		return 0
	}
	return l.Cost / l.Payout
}

// Position is a round's holdings. Either leg may be absent while legging in.
//
// Legs are tracked with independent payouts because real fills have them:
// staking equal DOLLARS at unequal prices buys unequal numbers of tokens. Equal
// size is the exception, not the rule.
type Position struct {
	Up   *Leg
	Down *Leg
}

func (p Position) Cost() float64 {
	var c float64
	if p.Up != nil {
		c += p.Up.Cost
	}
	if p.Down != nil {
		c += p.Down.Cost
	}
	return c
}

func (p Position) PayoutIfUp() float64 {
	if p.Up == nil {
		return 0
	}
	return p.Up.Payout
}

func (p Position) PayoutIfDown() float64 {
	if p.Down == nil {
		return 0
	}
	return p.Down.Payout
}

// Floor is guaranteed P&L -- the WORST branch. Positive means locked.
// A lock is a guarantee, so it is defined by the worst case, never the average.
func (p Position) Floor() float64 {
	return min(p.PayoutIfUp(), p.PayoutIfDown()) - p.Cost()
}

func (p Position) Ceiling() float64 {
	return max(p.PayoutIfUp(), p.PayoutIfDown()) - p.Cost()
}

func (p Position) IsLocked() bool {
	return p.Up != nil && p.Down != nil && p.Floor() > 0
}

// CompleteSets is the matched UP/DOWN pairs held -- redeemable for CASH
// IMMEDIATELY via prediCt's permissionless `merge`, not at resolution.
//
// Because each outcome token pays exactly $1, the payout figures ARE token
// counts, so matched pairs is the smaller of the two. This is the difference
// between a guarantee that pays in fifteen minutes and cash in the wallet now --
// which, on a small bankroll trading short rounds, is the difference between one
// position at a time and continuous redeployment.
func (p Position) CompleteSets() float64 {
	return min(p.PayoutIfUp(), p.PayoutIfDown())
}

// RealizedOnMerge is P&L banked immediately by merging. Equals Floor, but
// available now rather than at resolution.
func (p Position) RealizedOnMerge() float64 { return p.CompleteSets() - p.Cost() }

// Residual is unmatched tokens left after merging -- a free option on that side,
// and the part of the position that cannot be recycled. Zero when payouts are
// equalized, which is the real argument for equalizing them.
func (p Position) Residual() float64 {
	return math.Abs(p.PayoutIfUp() - p.PayoutIfDown())
}

// EqualizingHedgeCost is the hedge spend that makes both branches pay the same,
// which is provably the spend that maximizes the guaranteed floor.
//
// Writing the floor against hedge spend c: it rises at rate (1/h - 1) until the
// payouts match, then falls at rate 1. The maximum sits exactly at the kink.
// Overspending buys extra payout on a branch already covered -- paying certain
// money for an uncertain gain, backwards for a strategy built on the guarantee.
func EqualizingHedgeCost(entryPayout, hedgePrice float64) float64 {
	return entryPayout * hedgePrice
}

// FloorAtHedgeCost reports the guaranteed floor for a given hedge spend, for
// comparing plans before committing to one.
func FloorAtHedgeCost(entry Leg, hedgePrice, hedgeCost float64) float64 {
	if hedgeCost <= 0 {
		return -entry.Cost
	}
	hedgePayout := hedgeCost / hedgePrice
	return min(entry.Payout, hedgePayout) - entry.Cost - hedgeCost
}
