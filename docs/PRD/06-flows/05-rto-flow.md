# Flow — RTO (Return to Origin)

> Cuts across Features 09 (tracking), 10 (NDR), 11 (returns/RTO), 13 (wallet).

## Lifecycle

```mermaid
stateDiagram-v2
    [*] --> Forward_Shipment_NDR_Exhausted
    Forward_Shipment_NDR_Exhausted --> RTO_Initiated
    RTO_Initiated --> RTO_In_Transit
    RTO_In_Transit --> RTO_Delivered_To_Seller
    RTO_Delivered_To_Seller --> QC_Pending
    QC_Pending --> QC_Passed
    QC_Pending --> QC_Failed
    QC_Pending --> QC_Partial
    QC_Passed --> Buyer_Refund_Triggered
    QC_Failed --> Seller_Decides
    QC_Partial --> Buyer_Partial_Refund_Triggered
    Buyer_Refund_Triggered --> [*]
    Seller_Decides --> [*]
    Buyer_Partial_Refund_Triggered --> [*]
```

## Sequence

```mermaid
sequenceDiagram
    participant CRR as Carrier
    participant NDRC as NDR
    participant TR as Tracking
    participant ADP as Adapter
    participant WAL as Wallet
    participant SLR as Seller
    participant N as Notify
    participant Buyer

    NDRC->>ADP: request rto (or carrier auto-rto on max attempts)
    ADP->>CRR: rto API
    CRR-->>ADP: ack; rto_initiated
    ADP->>TR: status rto_initiated
    TR->>WAL: post rto_charge (debit to seller)
    TR->>N: notify seller + buyer
    CRR->>TR: rto_in_transit events
    CRR->>TR: rto_delivered_to_seller event
    SLR->>SLR: receive parcel; perform QC
    SLR->>SLR: mark QC outcome (pass/fail/partial)
    alt prepaid order
        SLR->>SLR: trigger buyer refund (channel-side)
        N->>Buyer: refund initiated
    else COD order
        Note over Buyer: no refund needed (buyer never paid)
    end
```

## Wallet treatment

| Event | Ledger entry |
|---|---|
| RTO initiated | `rto_charge` (debit to seller) |
| QC pass on prepaid | Seller initiates buyer refund externally; no Pikshipp wallet effect on buyer side |
| Insurance covers (lost / damaged in RTO leg) | `insurance_payout` (credit) |

## Open questions

- **Q-RTO1** — Auto-trigger buyer refund on prepaid RTO + QC pass? Possibly v2; integration with channels.
- **Q-RTO2** — RTO insurance bundle ("no-RTO insurance") — see Feature 22.
