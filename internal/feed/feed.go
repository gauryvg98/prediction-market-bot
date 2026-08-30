// Package feed supplies rounds to the paper engine.
//
// A Feed is the seam between "where the ticks come from" and "what the strategy
// does with them". The synthetic feed here lets the whole machine RUN today,
// deterministically, with no network. The live feed (Bitquery Transfers tape +
// a spot source) implements the same interface and drops in unchanged -- the
// engine cannot tell the difference, which is the point: paper results on the
// real tape are then directly comparable to these.
package feed

import "github.com/gauryvg98/prediction-bot/internal/paper"

// Round is one market's full life: an ordered series of observations (newest
// last, expiry approaching) and the outcome it finally resolved to.
type Round struct {
	Ticks      []paper.Tick
	Resolution paper.Resolution
}

// Feed yields rounds until exhausted. A live feed blocks; the synthetic feed
// returns immediately.
type Feed interface {
	Next() (*Round, bool)
}
