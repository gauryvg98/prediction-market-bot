# Bitquery for the Phase 0 tape — what works, tested live

Probed with a real key on 2026-08-28. Endpoint: `https://streaming.bitquery.io/eap`,
header `Authorization: Bearer <token>`. Turn any query into a live stream by
replacing `query` with `subscription`.

## The one finding that dictates every query

**Bitquery's `InstructionBalanceUpdates` stream does NOT cover Token-2022. The
`Transfers` API does.** Verified by controlled test:

| Query | Token | Result |
|---|---|---|
| `InstructionBalanceUpdates` on CASH | Token-2022 | **0 rows** |
| `InstructionBalanceUpdates` on USDC | classic SPL | 3 rows (control passes) |
| `Transfers` on CASH | Token-2022 | **3 rows** ✓ |

World's CASH and every outcome mint are Token-2022, so **the entire tape must come
from `Transfers`, never `InstructionBalanceUpdates`.** This is the thing that would
have silently returned empty tapes and wasted days if not isolated first.

Note: `Instruction.Accounts.Token.ProgramId` *does* resolve Token-2022 (that is how
the probe "passed"), but identifying an account's program is not the same as
indexing its balance changes. Only `Transfers` does the latter for Token-2022.

Also: filtering `InstructionBalanceUpdates` / `Transfers` by
`Instruction.Program.Address = prediCt` (or DFlow) returns nothing, because the
balance change is executed by the Token-2022 program, not the CPI caller. Filter by
**currency mint**, not by the calling program.

## Working queries

- `transfers_cash.graphql` — recent CASH legs, live (confirmed returning trades
  seconds old).
- `transfers_by_mint.graphql` — one round's full history: pass an outcome mint.

## Price extraction

A buy is one transaction carrying two transfers: CASH out from the taker, outcome
tokens in to the taker. Join the two legs by `Transaction.Signature`:

    price = CASH_out / outcome_tokens_in     (cost per $1 of payout)

Round discovery + resolution still come from prediCt `Instructions` (which DO
index): `initialize_market` gives the round's mints and window; the operator key's
resolve writes the winner.

## Limits that matter for how we run this

- Free/trial tiers rate-limit hard (~10 req/min). Fine for pulling a historical
  tape in batches; useless for a live trading loop.
- Broad `Transfers` on CASH with no time bound risks timeouts — it is high volume.
  Scope by mint (per round) or a short time window.
- Live execution never uses this — it uses own RPC + the self-route.
