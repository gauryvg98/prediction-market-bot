# Custody finding: the app's payer and position-holder are different accounts

Decoded from a real trade (`2qM2…PQy9b`) plus signature history of both accounts.
All from public mainnet RPC.

## What the trade showed

A DFlow `swap_with_destination` buy: **16.84 CASH → 68.70 outcome tokens at 0.245
(1:3.08)**, an underdog entry. But payer and holder are not the same account:

| Role | Account | Evidence |
|---|---|---|
| Signs + pays CASH | `EAhEjBhK…htCf5` | only owned balance change is −16.84 CASH |
| Receives the tokens | `DKkYnxbw…BUSaE` | `destination_token_authority`; +68.70 outcome tokens |

Across the signer's full history, it **never holds outcome tokens** — it is a
pay-only key. Every position is directed elsewhere.

## What the position-holder looks like

`DKkYnxbw` does not behave like a personal wallet. Its recent flows include exact
in-then-out pass-throughs (`+34970.76` then `−34970.76`) and token movements up to
~66,456 across multiple mints. That is intermediary / aggregation behavior, not a
$20-scale trader's position account.

**Caveat, stated honestly:** ~18 transactions is not proof of what `DKkYnxbw` is,
and a clean per-trade BUY/SELL tape could not be reconstructed here because the
CASH and token legs sit under different owners in the same transaction. Treat the
"intermediary" read as strongly-suggested, not settled.

## Why it matters for the bot

The strategy's instant-cash-out edge depends on `merge` — and `merge` burns a YES+NO
pair held by ONE authority. If positions placed through the Phantom app live under
an account the user does not sign for, the bot **cannot merge them**.

**Design rule (holds regardless of what `DKkYnxbw` turns out to be):** when the bot
self-routes, set `destination_token_authority` to the bot's OWN wallet. Both legs
then land under a key the bot controls, so it can merge on demand. This is a
concrete, second reason self-routing beats piggybacking on the app's flow — the
first was the limit price; this is custody.

## Open, if we want certainty on `DKkYnxbw`

Pull a few hundred of its transactions and check: does it fan out to many distinct
CASH payers (→ shared intermediary), or only ever pair with `EAhEjBhK` (→ this
user's dedicated custody)? Bounded RPC work, no credentials.
