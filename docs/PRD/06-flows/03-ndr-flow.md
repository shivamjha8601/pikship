# Flow — NDR action loop

> Cuts across Features 09 (tracking), 10 (NDR), 16 (notifications), 17 (buyer experience).

## Lifecycle

```mermaid
stateDiagram-v2
    [*] --> NDR_Detected
    NDR_Detected --> Buyer_Outreach_Sent
    Buyer_Outreach_Sent --> Buyer_Responded: pre-deadline
    Buyer_Outreach_Sent --> Seller_Action_Required: timeout
    Buyer_Responded --> Carrier_Action_Submitted
    Seller_Action_Required --> Carrier_Action_Submitted
    Seller_Action_Required --> Auto_Action_Per_Rule: rule fires
    Auto_Action_Per_Rule --> Carrier_Action_Submitted
    Carrier_Action_Submitted --> Reattempted: reattempt accepted
    Carrier_Action_Submitted --> RTO_Initiated: rto chosen
    Reattempted --> Delivered
    Reattempted --> NDR_Detected: still failed (loop)
    RTO_Initiated --> [*]
    Delivered --> [*]
```

## Detailed sequence

```mermaid
sequenceDiagram
    participant CRR as Carrier
    participant TR as Tracking
    participant NDRC as NDR ctx
    participant N as Notify
    participant Buyer
    participant FB as Feedback page
    participant ADP as Adapter
    participant SLR as Seller
    participant RUL as Rule engine

    CRR->>TR: NDR event (reason, attempt_no, location)
    TR->>NDRC: createNDREvent (deadline=+24h)
    NDRC->>N: enqueue buyer outreach (WhatsApp + SMS)
    N->>Buyer: "We missed you. Reschedule here: <link>"
    NDRC->>SLR: in-app notification

    alt buyer responds
        Buyer->>FB: opens, picks slot/address
        FB->>NDRC: action=reattempt_with_slot
    else seller acts
        SLR->>NDRC: pick action (reattempt / rto / contact)
    else timeout & rule
        RUL->>NDRC: auto-action per seller config
    end

    NDRC->>ADP: requestAction(awb, action, payload)
    ADP-->>NDRC: ack
    NDRC->>N: status update to seller and buyer

    CRR->>TR: delivered (or another NDR / rto)
    TR->>N: outcome notification
```

## Action-to-outcome matrix

| Action | Likely outcome |
|---|---|
| reattempt | ~50–70% deliver next attempt |
| reattempt_with_slot | +5–10% lift over generic reattempt |
| reattempt_with_address | High lift if original address was wrong |
| contact_buyer | Indirect; surfaces info |
| hold_at_hub | Buyer collects; ~30% conversion |
| rto | Terminal; cost to seller |

## Auto-rule examples

```yaml
# Conservative rule set
- if reason == buyer_unavailable and attempt_no <= 2: action=reattempt
- if reason == refused: action=rto
- if attempt_no >= max(carrier): action=rto

# Aggressive rule set (more saves; more friction)
- always send buyer outreach immediately
- if no response in 12h and reason==buyer_unavailable: action=reattempt_with_slot=morning
- if attempt_no >= 3: action=hold_at_hub if supported else rto
```

## Buyer page UX

(See `04-features/17-buyer-experience.md`.)

## Notes

- Carrier-specific limitations affect what actions are even available.
- WhatsApp template approval is the long pole; without it, outreach degrades to SMS.
- Buyer multi-language matters in tier-2/3 cities.
