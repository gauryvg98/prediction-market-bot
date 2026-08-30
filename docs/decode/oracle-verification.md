# Oracle verification — what was confirmed, and the honest wall

Goal: verify what price oracle World uses to resolve BTC up/down, by checking
resolved rounds against a reference feed. Worked from public Bitquery + RPC data.

## Confirmed on-chain (solid)

- **Resolution is `determine_outcome`**: disc `1871166631956d52`, 10-byte data =
  8-byte disc + a `u16` winner ∈ {0, 10000}. Operator-signed
  (`DDucv2De…`). No Chainlink/oracle account, no price, no signed report in the
  instruction. Matches the trust-model finding exactly.
- **Two Anchor events fire on resolution**, both keyed to a round pubkey:
  `493ee3c2…` (144 bytes) and `7e4928ee…` (50 bytes). The 50-byte one is
  `{round: pubkey, outcome: u16}`. The 144-byte one carries 96 bytes of
  additional fields.
- **Markets are pre-created and per-asset.** `initialize_market`
  (disc `2323bdc19b30aacb`) embeds asset metadata (e.g. `BTC-UP`/`BTC-DN`) and a
  `start_ms`/`end_ms` window in its data; some are created a day ahead of their
  window. Strike is NOT stored on-chain — consistent with **strike = oracle price
  at the round's start**.
- **Resolved Market accounts close within seconds** (rent reclaimed by the
  operator), so the post-resolution account state cannot be read after the fact.

## The wall (honest)

Two things blocked an independent per-round price check from public data:

1. **Round ↔ creation linkage is in-program.** The market pubkey in
   `determine_outcome` never appears in any `initialize_market` instruction's
   accounts (verified across account-includes queries with a wide time window).
   The link is a PDA derived inside the program — the same seed-derivation wall
   the source research documented as unsolved without disassembly.
2. **No settlement price recovered on-chain.** The `determine_outcome` data holds
   only the outcome. The 144-byte event's 96 field-bytes were NOT decodable into
   a price without the event schema; a scan produced only structural constants
   (2^56, 2^57 byte patterns), which were mistaken for prices in a first pass and
   then rejected. No confirmed price field.

## Status of the oracle claim

- **Mechanism: confirmed.** World writes only the winner on-chain; any price
  lives off-chain. This is fully consistent with World's stated "Chainlink Data
  Streams + CRE" design and with the Polymarket 5-minute precedent.
- **Venue: evidenced, NOT independently confirmed.** Chainlink Data Streams
  BTC/USD (a CEX-price aggregate) is the strong inference, but this session could
  not confirm it against resolved rounds, because the price is not in a decoded
  on-chain field and the round-linkage is in-program.

## The bounded task that would finish it

Decode the `493ee3c2…` event schema (likely
`MarketResolved{ round, strike, settle_price, ts, outcome }`). Its 96 field-bytes
almost certainly hold the settlement price. Once the layout is assigned, extract
the price per round and compare to Binance / Chainlink Data Streams BTC/USD at the
round's expiry. That both confirms the venue and measures the basis the bot would
trade against. It is a focused decode job, de-risked to a single event's layout.

## Why this does not block the bot

The resolution *mechanism* is confirmed, and the design rule the bot needs from it
already holds regardless of the exact venue: **the operator key resolves with no
on-chain proof, so minimize time held through resolution (merge on lock), and use
Chainlink Data Streams as the strike/distance reference for the vol gate.** The
venue confirmation refines the basis estimate; it does not gate paper trading.
