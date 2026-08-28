# prediction-bot

A selective bot for **World**'s BTC up/down prediction markets on Solana (the
markets Phantom surfaces). It buys one leg, hedges into a guaranteed lock when the
market moves, cuts when it doesn't, and **skips most rounds**.

Go first. Rust later, only if measurement shows latency is what's costing money.

---

## Status

| Package | What it does | Tests |
|---|---|---|
| `internal/market` | Price space, `S = p_up + p_down`, fees → one breakeven scalar | ✅ |
| `internal/vol` | Implied vol from a binary quote, realized vol, touch probability | ✅ |
| `internal/signal` | **The entry gate.** Default is skip | ✅ |
| `internal/lock` | Barrier maths, positions, complete sets, hedge sizing | ✅ |
| `internal/world` | Program IDs, market layout, discriminators | constants |

```bash
make test     # all green
make gate     # watch it decide on synthetic rounds
```

Nothing touches the network yet. No keys, no orders.

## The strategy, honestly

**What is guaranteed.** World's market program mints and burns outcome tokens only
as equal pairs at par — `1 CASH ⇔ 1 UP + 1 DOWN`, permissionless and fee-free. So
once both legs are held for less than 1.00 total, the profit is **certain**,
enforced by the vault rather than promised by a counterparty. Better still,
matched pairs can be `merge`d back to CASH *immediately* rather than waiting for
the round to resolve — the capital recycles into the next setup.

**What is not.** Getting the second leg. At any instant the maker quotes
`p_up + p_down = 1 + v`, so a hedge that locks only appears once the leg already
held has *risen* — i.e. once it is winning. No detector knows a chart will swing.

**So the edge has to come from selection.** Buying a cheap leg and hedging when it
moves is a **long-volatility** position: it pays when the market travels further
than the price charged for. That is decidable, because a binary's price *is* an
implied probability and can be inverted:

1. Invert the quote → the market's **implied** volatility.
2. Measure **realized** volatility from recent spot.
3. Convert the lock barrier from price space into a BTC level.
4. Compute **P(touch that level)** under realized vol.
5. Compare against the fair-market ceiling, `entry / barrier`.

Step 5 is the whole discipline. In a fairly-priced market the touch probability
**cannot** exceed `entry_price / barrier_price` — above that, the implied win rate
among unhedged rounds would have to be negative. That ratio is the bar. A setup
earns an entry by clearing it, and is skipped otherwise.

**And losers get cut.** Outcome tokens are ordinary tokens with a continuous maker,
so a leg that goes the wrong way is sold, not carried to expiry. That turns the
tail from "occasional total loss" into a stop-loss we control — the single biggest
improvement available over trading this by hand.

## What I need from you

### 1. A private RPC — yes, genuinely required

Public mainnet RPC will rate-limit `getProgramAccounts` immediately and is too slow
and too unreliable for submitting transactions. **Start with [Helius](https://helius.dev)** —
best Solana developer experience, a free tier that covers Phase 0 comfortably, and
staked-connection sending that matters a lot for landing transactions during
congestion. [QuickNode](https://quicknode.com) and [Triton](https://triton.one) are
equivalent choices.

Put both HTTP and WS URLs in `.env` (see `.env.example`).

### 2. DFlow API key — the hard gate, apply today

Single-leg buys and sells route through DFlow's Trading API, which needs an
`x-api-key` with **2–5 day approval** (`pond.dflow.net/build/api-key`). The keyless
dev endpoint returns `route_not_found` for World's mints, so there is no way around
it. **Nothing live works without this**, and its lead time is the long pole — apply
before anything else.

### 3. Bitquery token — optional, saves real work

Free trial is enough. Used only for the Phase 0 historical tape; the same data is
reachable over raw RPC with more code. Skip if you'd rather not add a dependency.

### 4. A dedicated bot wallet

**You generate it and you hold the key.** Nothing in this repo generates, prints,
or transmits key material. Fund it with the working bankroll and a little SOL for
fees — never a main wallet. See §Risk: positions on this venue are seizable by
design.

### 5. Two decisions

- **Round length.** 15-minute rounds are the better target and you already
  suggested them: more time for a move to reach the barrier, and a third the
  transaction cost per hour. 5-minute rounds are the same code with worse
  economics.
- **Starting bankroll.** $20 works for proving the thing out. Be aware fees are
  per-transaction and indifferent to size, so the same strategy is meaningfully
  more viable at $200 — see the cost note below.

### A suggestion you didn't ask for, on the price feed

World resolves via **Chainlink**, not Binance. For *volatility* any liquid feed is
fine — vol is similar everywhere. But *distance to strike* drives the binary price
and the barrier, and near the strike a few dollars of basis flips an outcome. So
the signal should read spot from the same source that decides the round, or at
minimum measure the basis and refuse to trade when it is wide. Worth settling
before going live.

## Roadmap

**Phase 0 — measurement. No DFlow key needed. Answers go/no-go at zero risk.**
Reconstruct the price tape from chain (every maker fill is a transaction whose
balance deltas give the realized price), replay it through the gate, and count how
often entries actually reach a lock versus the fair ceiling. This is what decides
whether the edge exists. Roughly a day of observation at 15-minute rounds.

**Phase 1 — execution.** Quote poller, transaction builder, position tracker,
merge sweeper, stop-loss unwinds.

**Phase 2 — Rust,** only if Phase 0/1 latency data says the round trip is what is
costing money. Not before.

## Cost and risk

**Transactions.** Three per round (entry, hedge, merge) at ~5,000 lamports base plus
priority. Per round that is trivial against a 2.5% spread. Across 96 rounds/day
(15-minute) it is a real line item on a small bankroll — which is the argument for
being selective, not for trading faster.

**Counterparty.** Stated plainly because it is the reason to fund a bot wallet and
nothing more:

- **Resolution is one key.** `determine_outcome` writes a 2-byte winner with no
  oracle account, price feed, or proof in the instruction. No dispute window, no
  refund path.
- **Positions are seizable by design.** Outcome tokens carry a Token-2022 permanent
  delegate — the mechanism used to burn the losing side at settlement. The same
  power can move or destroy any position.
- **CASH is Bridge's.** The issuer holds freeze and clawback over every account.
- **All four programs are upgradeable**, and World's in-house maker shares prediCt's
  upgrade key.

**Addresses in `internal/world` come from independent research, not official docs.**
Re-verify against mainnet before they carry money.
