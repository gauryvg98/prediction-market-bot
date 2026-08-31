// Ledger accumulates every paper round and reports the statistics that cannot
// flatter -- locks and naked expiries side by side, and the one comparison that
// decides whether the edge is real.
package paper

import (
	"fmt"

	"github.com/gauryvg98/prediction-bot/internal/signal"
)

// Ledger is the running record of a paper session.
type Ledger struct {
	Events []Event

	// gate diagnostics: how the entry filter behaved, so a session that never
	// trades can still explain why.
	Evaluated   int
	Entered     int
	SkipReasons map[string]int

	// terminal round tallies. A round is counted once, at its terminal event.
	Locks      int     // hedged then merged -- guaranteed profit banked
	NakedWins  int     // unhedged, resolved our way
	NakedLoss  int     // unhedged, resolved against -- the whole stake
	Staked     float64 // total CASH put at risk on entry legs
	RealizedPn float64 // net paper P&L across all terminal rounds
	WorstRound float64 // most negative single-round P&L -- the tail, surfaced
}

func NewLedger() *Ledger { return &Ledger{SkipReasons: map[string]int{}} }

func (l *Ledger) observe(d signal.Decision) {
	l.Evaluated++
	if d.Enter {
		l.Entered++
		return
	}
	// bucket the skip by its leading category (first token of the reason)
	r := d.Reason
	for i := 0; i < len(r); i++ {
		if r[i] == ' ' || r[i] == ':' {
			r = r[:i]
			break
		}
	}
	if r == "" {
		r = "unknown"
	}
	l.SkipReasons[r]++
}

func (l *Ledger) record(e Event) {
	l.Events = append(l.Events, e)
	switch e.Kind {
	case Enter:
		l.Staked += e.Price * e.Qty // == stake
	case Merge:
		l.Locks++
		l.book(e.PnL)
	case Settle:
		if e.PnL >= 0 {
			l.NakedWins++
		} else {
			l.NakedLoss++
		}
		l.book(e.PnL)
	}
}

func (l *Ledger) book(pnl float64) {
	l.RealizedPn += pnl
	if pnl < l.WorstRound {
		l.WorstRound = pnl
	}
}

// Terminal is the number of rounds that reached a final state.
func (l *Ledger) Terminal() int { return l.Locks + l.NakedWins + l.NakedLoss }

// LockRate is the fraction of ENTERED rounds that reached a lock. The single
// most important number in the strategy -- and the one that cannot be estimated
// by recalling how it felt.
func (l *Ledger) LockRate() float64 {
	if t := l.Terminal(); t > 0 {
		return float64(l.Locks) / float64(t)
	}
	return 0
}

// ReturnOnStake is net P&L per $1 of entry stake -- the honest headline.
func (l *Ledger) ReturnOnStake() float64 {
	if l.Staked > 0 {
		return l.RealizedPn / l.Staked
	}
	return 0
}

// Summary is a one-block human report. Locks and naked expiries are never shown
// apart, and the worst round is always surfaced, because the strategy's risk
// lives entirely in the tail it is tempting to forget.
func (l *Ledger) Summary() string {
	return fmt.Sprintf(
		"evaluated %d  entered %d (%.1f%%)  |  terminal %d: locks %d / naked-win %d / naked-loss %d\n"+
			"lock rate %.1f%%  |  staked %.2f  net P&L %+.2f  return/stake %+.1f%%  worst round %+.2f",
		l.Evaluated, l.Entered, pct(l.Entered, l.Evaluated),
		l.Terminal(), l.Locks, l.NakedWins, l.NakedLoss,
		l.LockRate()*100, l.Staked, l.RealizedPn, l.ReturnOnStake()*100, l.WorstRound)
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}
