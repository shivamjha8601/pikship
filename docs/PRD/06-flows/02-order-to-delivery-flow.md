# Flow — Order to delivery (the canonical happy path)

> Cuts across Features 03–17.

## Stages

```mermaid
flowchart LR
    A[Channel order] --> B[Ingestion]
    B --> C[Canonical Order created]
    C --> D[Validation + auto-rules]
    D --> E[Ready_to_ship]
    E --> F[Rate quote + carrier select]
    F --> G[Two-phase wallet reserve]
    G --> H[Carrier book]
    H --> I[AWB + label]
    I --> J[Manifest + pickup]
    J --> K[Picked_Up]
    K --> L[In_Transit]
    L --> M[Out_For_Delivery]
    M --> N[Delivered]
    N --> O[Buyer notified + COD remittance flow]
```

## Detailed sequence

```mermaid
sequenceDiagram
    participant CH as Shopify
    participant ING as Ingestion
    participant OMS
    participant RUL as Rule engine
    participant SLR as Seller (UI)
    participant RAT as Rate engine
    participant WAL as Wallet
    participant BK as Booking
    participant ADP as Carrier adapter
    participant CRR as Carrier
    participant TR as Tracking
    participant N as Notifications
    participant Buyer
    participant TPG as Tracking page

    CH->>ING: webhook order/create
    ING->>OMS: canonical Order
    OMS->>RUL: apply auto-rules
    alt auto-book
        RUL->>BK: book (per rule)
    else manual
        OMS-->>SLR: appears in dashboard
        SLR->>BK: book
    end
    BK->>RAT: quote
    BK->>WAL: reserve
    BK->>ADP: book
    ADP->>CRR: create shipment
    CRR-->>ADP: AWB
    BK->>WAL: confirm
    BK->>N: notify buyer (booked)
    N->>Buyer: WhatsApp + tracking link
    Buyer->>TPG: opens
    CRR->>TR: pickup event
    TR->>N: notify seller (picked up)
    CRR->>TR: in transit events
    CRR->>TR: OFD
    TR->>N: notify buyer (OFD)
    N->>Buyer: WhatsApp OFD
    CRR->>TR: delivered
    TR->>N: notify buyer + seller
    N->>Buyer: delivered + rate request
```

## Multi-shipment orders

If the order is split into multiple shipments:
- Each shipment runs independently.
- Order status is `partially_fulfilled` until all are delivered.

## Cancellation paths

| Cancelled at | Effect |
|---|---|
| Channel side, before our ingest | We don't ingest |
| Channel side, after ingest, before book | Mark cancelled; remove from queue |
| After book, before pickup | Try carrier cancel; refund wallet on success |
| After pickup | Cannot cancel; process as RTO if needed |

## Edge variant: COD

(See `04-cod-remittance-flow.md` for the cash-side flow.)

## Edge variant: NDR before delivered

(See `03-ndr-flow.md`.)
