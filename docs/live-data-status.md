# Live data wiring — status

What runs, what's real, and the one honest gap. All from public Bitquery data.

## Working, on real live data

- **`internal/bitquery`** — Go client for Bitquery's Solana GraphQL API
  (`streaming.bitquery.io/eap`, bearer token). Read-only market data; no key here
  moves funds.
- **`cmd/tape`** — pulls the live World tape and reconstructs **priced outcome
  trades**: `price = CASH / outcome-tokens`, cost per $1 of payout. Verified
  against live mainnet (e.g. a real SELL at 0.3568 for $8.99).
- **Outcome-leg isolation is solved.** World routes trades through multi-hop
  paths (USDC ramp, wrapped SOL, intermediates). The outcome leg is identified
  by **program**: outcome tokens are Token-2022, which cleanly excludes the SOL
  (system) and USDC (classic SPL) hops. Unit-tested in `tape_test.go`.

## Two real constraints found

1. **Per-signature reconstruction is query-heavy.** Pricing a trade needs the
   transaction's full legs — one query per trade. Bursts hit Bitquery's
   per-minute rate limit (HTTP 429). **Production must use bulk
   `transfers_by_mint`** (one query per outcome mint, hundreds of fills) instead
   of per-signature expansion. The per-sig path is fine for spot checks, not for
   a continuous feed.
2. **Round pairing is the remaining gap.** A full two-leg paper measurement needs
   both sides of a round (UP mint + DOWN mint) plus its strike and resolution.
   Enumerating a round's mint PAIR is blocked the same way everything else is —
   World's round identity is derived in-program and its `initialize_market`
   linkage did not resolve from public instruction accounts.

## The clean unlock (bounded)

Pair mints by **complementary pricing**: the two sides of one round have prices
that sum to ~1 + vig at the same timestamps. Discover mint pairs from the tape
itself (no init, no PDA derivation), then pull each mint's full series via
`transfers_by_mint`, align by time into `market.Book` ticks, and feed the paper
engine. Strike/resolution come from Binance BTC over the round window (World
resolves on a CEX-price oracle). `cmd/tape` already prints candidate pairs; the
step left is wiring confirmed pairs into a `feed.Bitquery` behind the existing
`feed.Feed` interface — at which point the SAME paper engine runs on real fills.

## Bottom line

The bot runs end to end on the synthetic feed (tested). The Go Bitquery client is
wired and pulls **real, correctly-priced** World trades today. The last mile —
turning that real tape into fully-labeled two-leg rounds — is a bounded
data-engineering task (bulk-by-mint + complementary pairing), not a new wall.
