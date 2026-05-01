# Flow — Weight dispute

> Cuts across Features 08 (booking), 14 (weight reconciliation), 13 (wallet).

## Capture phase (at packing)

```mermaid
sequenceDiagram
    actor Op as Seller Operator
    participant UI
    participant ST as Storage
    participant SH as Shipment

    Op->>UI: pack order, snap photo of parcel + scale
    UI->>ST: upload photo (compressed)
    ST-->>UI: ref
    UI->>SH: attach evidence ref to shipment
```

## Dispute phase (post-pickup)

```mermaid
sequenceDiagram
    participant CRR as Carrier
    participant ING as Reweigh ingest
    participant EVAL as Auto-eval
    participant LED as Ledger
    participant ADP as Adapter
    participant N as Notify
    participant SLR as Seller

    CRR->>ING: reweigh data (awb, weight_g, photo_optional)
    ING->>EVAL: evaluate variance + evidence
    alt small variance
        EVAL->>LED: auto-accept charge
        EVAL->>N: inform seller (info)
    else seller evidence strong
        EVAL->>LED: post charge
        EVAL->>ADP: dispute
        ADP->>CRR: dispute submission
    else needs seller
        EVAL->>SLR: open dispute, await evidence/decision
    end
```

## Resolution phase

```mermaid
flowchart TD
    A[Carrier responds to dispute] --> B{Outcome}
    B -->|seller won| C[Post weight_dispute_reversal credit]
    B -->|carrier won| D[Charge stands; close]
    B -->|partial| E[Partial reversal credit]
    C --> F[Notify seller]
    D --> F
    E --> F
```

## SLA & policy

- Carrier dispute window varies (typ. 7–15 days from charge raised).
- Pikshipp SLA: auto-disputes filed within 24h of carrier reweigh data.
- Seller must add evidence within window if open-for-seller status.

## Open questions

(See Feature 14.)
