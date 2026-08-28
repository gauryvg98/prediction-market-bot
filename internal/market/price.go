// Package market is the price-space foundation: one unit, one invariant.
//
// Every price here is CASH per $1 of payout. A contract paying $1 if BTC is up
// in 15 minutes, bought for 0.40, has price 0.40 -- which is also the market's
// implied probability of that outcome. Working in this unit (rather than odds)
// collapses the whole strategy to a single scalar:
//
//	S = PriceUp + PriceDown
//
//	S < 1  -> owning both sides returns more than it cost. Guaranteed.
//	S = 1  -> fair and frictionless.
//	S > 1  -> the maker's overround; S-1 is what it earns for quoting.
//
// This is not a modelling convention. World's market program (prediCt) mints and
// burns outcome tokens ONLY as equal pairs at par -- 1 CASH <-> 1 UP + 1 DOWN,
// permissionless and fee-free. So a complete set is worth exactly 1.00 by
// protocol, and acquiring both legs for less than 1.00 is profit enforced by the
// vault rather than promised by a counterparty. S < 1 is an on-chain invariant.
package market

import (
	"fmt"
	"math"
)

// Outcome is one of the two mutually exclusive, exhaustive sides.
type Outcome uint8

const (
	Up Outcome = iota
	Down
)

func (o Outcome) String() string {
	if o == Up {
		return "UP"
	}
	return "DOWN"
}

// Other returns the side that pays when o does not -- the hedge leg.
func (o Outcome) Other() Outcome {
	if o == Up {
		return Down
	}
	return Up
}

// PriceFromOdds converts "to-one" odds into cost per $1 of payout.
// toOne=3 means stake 1 to win 3 (return 4), so price = 1/4 = 0.25.
func PriceFromOdds(toOne float64) (float64, error) {
	if toOne <= 0 || math.IsNaN(toOne) {
		return 0, fmt.Errorf("odds must be positive, got %v", toOne)
	}
	return 1 / (1 + toOne), nil
}

// OddsFromPrice is the inverse -- the "N" in 1:N. Display only.
func OddsFromPrice(price float64) (float64, error) {
	if price <= 0 || price >= 1 {
		return 0, fmt.Errorf("price must be in (0,1), got %v", price)
	}
	return (1 - price) / price, nil
}

// Fees models venue costs in the two shapes that actually occur, and collapses
// them into the single threshold every decision compares against.
//
// World's makers embed a 2-3% spread in the quote rather than charging
// separately, so StakeRate is the field that usually carries it.
type Fees struct {
	StakeRate  float64 // fee on money in:  cost   -> cost * (1+r)
	PayoutRate float64 // fee on money out: payout -> payout * (1-r)
}

// BreakevenSum is the largest S that still locks a profit.
//
// Buying q of each side costs q*S*(1+StakeRate) and returns q*(1-PayoutRate)
// whichever way it resolves, so profit is positive exactly when
// S < (1-PayoutRate)/(1+StakeRate). With no fees this is 1.0, recovering the
// clean S < 1 rule. This is the ONLY threshold any engine compares against --
// fees are never re-derived at a call site.
func (f Fees) BreakevenSum() float64 {
	return (1 - f.PayoutRate) / (1 + f.StakeRate)
}

// Level is one executable price level.
type Level struct {
	Price float64
	Size  float64
}

// Book is both sides of one round at one instant. Ladders are best-first
// (cheapest), since this system is only ever a BUYER of payout on both sides.
//
// Timestamps ride on the book rather than coming from a clock, so every consumer
// stays pure and replayable against a recorded tape.
type Book struct {
	RoundID  string
	TsMs     int64
	ExpiryMs int64
	Up       []Level
	Down     []Level
}

func (b *Book) best(side []Level) (float64, bool) {
	if len(side) == 0 {
		return 0, false
	}
	return side[0].Price, true
}

func (b *Book) Best(o Outcome) (float64, bool) {
	if o == Up {
		return b.best(b.Up)
	}
	return b.best(b.Down)
}

// Sum returns S = PriceUp + PriceDown -- the number everything is organized
// around. ok is false if either side is unquoted.
func (b *Book) Sum() (s float64, ok bool) {
	u, uok := b.Best(Up)
	d, dok := b.Best(Down)
	if !uok || !dok {
		return 0, false
	}
	return u + d, true
}

// Overround is S-1: the maker's edge per $1 of payout. Positive in any healthy
// market. Negative is a genuine, free, simultaneous arbitrage.
func (b *Book) Overround() (float64, bool) {
	s, ok := b.Sum()
	return s - 1, ok
}

// MsToExpiry is how long this round has left.
func (b *Book) MsToExpiry() int64 { return b.ExpiryMs - b.TsMs }

// IsRiskFreeLock reports whether both sides can be bought right now for a
// guaranteed profit. Rare -- it means the venue's two-sided quote is inverted --
// but free when it happens, so it costs nothing to keep checking.
func (b *Book) IsRiskFreeLock(f Fees) bool {
	s, ok := b.Sum()
	return ok && s < f.BreakevenSum()
}
