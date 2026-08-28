# How to actually buy one leg

Correcting an earlier claim in this repo's README that a DFlow API key was the only
way to trade. **It is not.** DFlow's on-chain IDL shows the gate is informational,
not cryptographic.

## The evidence

`DF1ow4tspfHX9JwWJsAb9epbkA8hmpSEAtxXy1V27QBH` publishes its IDL on-chain. Two
things in it settle the question.

**1. `swap` has exactly one signer — the user.**

```
swap (6 accounts)
  token_program, associated_token_program, system_program,
  user_token_authority   <- SIGNER, and the only one
  event_authority, program

SwapParams { actions: Vec<Action>, quoted_out_amount: u64,
             slippage_bps: u16, platform_fee_bps: u16 }
```

No maker co-signature. The maker does not have to be online, does not have to
agree, and cannot refuse. `quoted_out_amount` and `slippage_bps` are **our** bounds,
enforced by the program — a limit price, not a maker-signed quote.

(DFlow does have a true RFQ path, `open_order` / `fill_order`, where a separate
`filler` signs. World trades do not use it — they enter as `swap` /
`swap_with_destination`.)

**2. The prediction-market route is a published Action variant.**

`Action` is a 75-variant enum, and two of them are ours:

```
OpenPredictionsOrderOptions {
    nonce: u64
    order_outcome: u8          <- which side: UP or DOWN
    quoted_out_amount: u64     <- how many outcome tokens we demand for our CASH
    slippage_bps: u16
    platform_fee_recipient_vault: pubkey
    platform_fee_scale: u16
}

BisonFiSwapOptions { amount: u64, orchestrator_flags: OrchestratorFlags }
```

That is the whole instruction payload for a prediction-market order, and it is
public. Picking a side and naming a price is fully expressible without asking
anyone.

## So what does the API key actually buy?

Three conveniences, none of them permission:

1. **Price discovery** — what `quoted_out_amount` is achievable right now.
2. **A pre-built transaction**, with the remaining accounts already assembled.
3. **Maker selection** — routing to whichever of JanusFI / BisonFI is better.

## What self-routing costs

One real piece of work: **the remaining-accounts layout** for the prediction action.
The IDL gives the instruction *data* encoding but not the account ordering the
route step expects. That is recoverable by decoding real Phantom trades on
mainnet — public transactions, the same ones the price tape already parses. Days
of work, not hours, but entirely tractable and needing nobody's permission.

## The part that favours self-routing

`quoted_out_amount` is a **limit price**, and this strategy is a limit-price
strategy. We are not trying to take whatever the maker is showing; we are waiting
for a specific favourable price and skipping the round otherwise. Naming the price
and getting filled-or-not *is* the design:

> "Fill me at 1:3 on the underdog, or don't fill me."

Taking a quoted RFQ price is strictly worse for this: it makes us a price taker in
a strategy whose entire edge is patience. Self-routing is not just the fallback —
for the entry leg it is arguably the better mechanism.

The counterweight is that a limit that never fills is a round skipped, and price
discovery still matters for setting a limit that is aggressive enough to trade and
cheap enough to be worth trading. That is what the Phase 0 tape measures.

## Ranked paths

| | Path | Cost | Verdict |
|---|---|---|---|
| **A** | DFlow production API key | free, 2–5 day approval | **Still apply.** Removes all reverse engineering. Zero downside to having it. |
| **B** | Self-routed `swap` + `OpenPredictionsOrder` | days of decoding | **The real answer.** Permissionless, no rate limits, no third-party dependency, and a limit price by construction. |
| **C** | Extracting Phantom's API key from the app | — | **No.** Those are credentials issued to someone else. Decoding public on-chain transactions to learn the protocol is a different thing and is fine — that data is public by construction. |

Do **A** and **B** in parallel. A is a form and a wait; B is the thing that makes
the bot independent of anyone's approval.

## Open item

There is no `JanusFiSwap` variant — only `BisonFiSwap` and the generic
`OpenPredictionsOrder`. So World's in-house maker is likely reached through the
generic prediction path while the independent maker has its own. Worth confirming
when decoding a live trade, since it determines which maker we can reach and
whether we can choose.
