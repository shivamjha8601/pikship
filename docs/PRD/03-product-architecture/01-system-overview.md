# System overview (logical architecture)

> This is a **product** architecture document, not a technical one. It defines the *logical* shape of the system — what the parts are, how they relate, and what each is responsible for. The technical realization (services, languages, data stores, queues, infra) lives in the HLD/HLA.

## What this document is for

- Anyone reading a feature spec downstream can refer here to understand *where* a feature plugs in.
- Anyone designing the technical architecture starts here for the boundaries.
- Anyone debating "should this be one product surface or two?" uses this as the source of truth on product seams.

## The big picture

```mermaid
flowchart TB
    subgraph SRC["Order sources (channels)"]
        SH[Shopify]
        WC[WooCommerce]
        MG[Magento]
        AM[Amazon SP-API]
        FK[Flipkart]
        ME[Meesho]
        CST[Custom storefronts]
        CSV[CSV / Excel import]
        MAN[Manual entry]
    end

    subgraph CORE["Pikshipp platform"]
        direction TB
        IAM[Identity & onboarding]
        SEL[Seller org & config]
        POL[Policy engine]
        OMS[Order management]
        CAT[Catalog & warehouses]
        ALC[Allocation engine]
        PRC[Pricing engine]
        BOOK[Shipment booking]
        TRACK[Tracking & status normalization]
        NDR[NDR action engine]
        RTO[Returns & RTO]
        COD[COD management]
        WAL[Wallet, billing, ledger]
        REC[Weight reconciliation]
        REPO[Reports & analytics]
        NOT[Notifications]
        BUYUI[Buyer experience surfaces]
        SUP[Support & ticketing]
        ADM[Admin & ops console]
        RSK[Risk & fraud]
        CON[Contracts & documents]
        AUD[Audit & change-log]
        INS[Insurance optional]
    end

    subgraph CN["Courier network (carrier adapters)"]
        DLV[Delhivery]
        BLD[Bluedart]
        DTDC[DTDC]
        EKT[Ekart]
        XPB[Xpressbees]
        ECM[Ecom Express]
        SHX[Shadowfax]
        IPS[India Post]
        MORE[...more adapters over time]
    end

    subgraph DEST["Sinks"]
        BUY[Buyers — WhatsApp / SMS / Email / Tracking page]
        FIN[Finance systems — Tally / Zoho / GST]
        DW[Pikshipp data warehouse]
    end

    SH & WC & MG & AM & FK & ME & CST & CSV & MAN --> OMS

    POL -.governs.-> CORE
    AUD -.records.-> CORE
    IAM -.scopes.-> SEL
    SEL --> POL
    OMS --> ALC
    ALC --> PRC
    ALC --> BOOK
    BOOK --> CN
    CN --> TRACK
    TRACK --> NDR
    TRACK --> RTO
    BOOK --> WAL
    COD --> WAL
    REC --> WAL
    NDR --> NOT
    TRACK --> NOT
    NOT --> BUY
    BOOK --> NOT
    OMS --> CAT
    OMS --> REPO
    REPO --> DW
    WAL --> FIN
    INS --> BOOK
    SUP --> ADM
    RSK -.signals.-> OMS
    RSK -.signals.-> ALC
    RSK -.signals.-> COD
    CON -.terms drive.-> SEL
    ADM -.controls.-> CORE
```

> The diagram is also saved as [`../diagrams/01-system-overview.mmd`](../diagrams/01-system-overview.mmd) and rendered to [`01-system-overview.png`](../diagrams/01-system-overview.png).

## The five conceptual layers

Reading the diagram top-to-bottom, the platform has five conceptual layers:

1. **Source layer (channels)** — where orders originate. We do not own this layer; we adapt to it.
2. **Core platform** — what we build, own, and operate. The substance of this PRD.
3. **Carrier layer** — the courier partners. We do not own this layer either; we abstract it through adapters.
4. **Sink layer** — where data flows out: to buyers (notifications), finance systems (export), our own analytics warehouse.
5. **Cross-cutting layer** — identity (scopes everything), policy engine (parameterizes everything), audit (records everything), admin/ops (controls everything), risk (signals into many).

## Two engines worth their own pages

Two of the boxes in the core warrant special call-outs because they are central to how the platform *behaves*:

### The Policy engine
The runtime system that resolves "what's the rule for *this* seller × *this* setting?". Every feature in the platform reads from it — the policy engine is what makes Pikshipp configurable without forking. Detailed in [`05-policy-engine.md`](./05-policy-engine.md).

### The Allocation engine
The runtime system that picks which carrier/service to use for a given shipment, given filter constraints (seller's allowed carriers, pincode serviceability, weight limits) and weighted objectives (cost, speed, reliability, seller preference). Auditable: every decision stores why. Detailed in [`../04-features/25-allocation-engine.md`](../04-features/25-allocation-engine.md).

## Bounded contexts (the seams)

A **bounded context** is a region of the product where a single ubiquitous language applies — an Order in OMS context is unambiguous, but the same word in Tracking has slightly different shape. We define the contexts so cross-context contracts become explicit.

```mermaid
graph LR
    subgraph BC["Bounded contexts"]
        IDC[Identity & seller-config]
        ORD[Order context]
        CAT2[Catalog context]
        ALC2[Allocation context]
        SHP[Shipment context]
        TRC[Tracking context]
        NDC[NDR/RTO context]
        FIN2[Financial context]
        REC2[Reconciliation context]
        NOT2[Notification context]
        SUP2[Support context]
        EXT[Channel/Carrier integration context]
        RSK2[Risk context]
        CON2[Contract/Document context]
        AUD2[Audit context]
    end
    IDC --- ORD
    ORD --- CAT2
    ORD --- ALC2
    ALC2 --- SHP
    SHP --- TRC
    TRC --- NDC
    SHP --- FIN2
    FIN2 --- REC2
    SHP --- NOT2
    EXT --- ORD
    EXT --- SHP
    EXT --- TRC
    SUP2 --- ORD
    SUP2 --- SHP
    RSK2 --- ORD
    RSK2 --- ALC2
    CON2 --- IDC
    AUD2 -.observes.- IDC
    AUD2 -.observes.- ORD
    AUD2 -.observes.- SHP
    AUD2 -.observes.- FIN2
```

### Context responsibilities (one-liner each)

| Context | Owns | Doesn't own |
|---|---|---|
| **Identity & seller-config** | Sellers, users, roles, sessions, seller-level configuration vector | Anything domain-specific |
| **Order** | Canonical Order, addresses, line items, channel ref | Shipping decisions |
| **Catalog** | SKUs, weights, dims, HSN codes | Order state |
| **Allocation** | Carrier choice per shipment, with scoring + audit | Booking mechanics |
| **Shipment** | AWBs, manifests, labels, attempts | Buyer notifications, ledger |
| **Tracking** | Status events, normalized statuses, ETA | Action decisions |
| **NDR/RTO** | NDR actions, RTO state, attempt budgets | Ledger entries |
| **Financial** | Wallet balances, invoices, ledger, GST, reverse-leg charges | Decisions to charge |
| **Reconciliation** | Weight disputes, courier invoice match, COD remittance match | Originating charges |
| **Notification** | Templates, sends, deliverability | Triggering events |
| **Support** | Tickets, conversations, escalations | Resolution actions |
| **Integration** | Channel adapters, courier adapters, credentials | Domain semantics |
| **Risk** | Risk scores, behavioral signals, fraud queues | Enforcement actions (signals to others) |
| **Contract/Document** | Contracts, KYC docs, machine-readable terms | Policy resolution (those values feed Identity) |
| **Audit** | Append-only event log, tamper-evidence, exports | Anything else |

### Why these seams?

Each seam is chosen so that:
1. Either side can change implementation without affecting the other.
2. The cross-context contract is small enough to write down (we do — see each feature's "data model" section).
3. Per-seller scoping happens at every seam crossing.

## Anatomy of a request: "book a shipment"

A worked example of how a single user action traces through contexts.

```mermaid
sequenceDiagram
    actor Op as Seller Operator (P7)
    participant UI as Seller dashboard
    participant API as API gateway
    participant SCO as Seller scope guard
    participant ORD as Order ctx
    participant ALC as Allocation engine
    participant PRC as Pricing engine
    participant BK as Shipment ctx
    participant ADP as Carrier adapter
    participant CRR as Courier API
    participant WAL as Financial ctx
    participant NOT as Notification ctx
    participant AUD as Audit

    Op->>UI: Click "Book"
    UI->>API: POST /shipments {orderId, ...}
    API->>SCO: validate token + seller scope
    SCO-->>API: ok
    API->>ORD: load order (seller-scoped)
    ORD-->>API: Order
    API->>ALC: pick(order)
    ALC->>PRC: rates(order, candidates)
    PRC-->>ALC: rates
    ALC-->>API: chosen carrier + audit explain
    API->>WAL: reserve(rate.amount)
    WAL-->>API: hold-id
    API->>BK: createShipment(order, carrier, rate)
    BK->>ADP: book(order, addr, weight, dims, COD)
    ADP->>CRR: courier-specific book request
    CRR-->>ADP: AWB + label URL
    ADP-->>BK: normalized booking result
    BK->>WAL: confirm(hold-id) → debit
    WAL-->>BK: ledger entry id
    BK-->>API: Shipment {AWB, label}
    API-->>UI: 200 {AWB, label url}
    BK->>NOT: emit ShipmentBooked event
    NOT->>NOT: enqueue buyer + seller notifications
    AUD-->>AUD: every step audit-logged
```

Things to note:
- **Seller scope is enforced at one well-defined point** (the API gateway / scope guard). Downstream contexts trust the request is in scope.
- **The wallet is debited via two-phase commit**: reserve → confirm/release. Avoids charging a seller for a booking that fails at the courier API.
- **Allocation is its own step** before booking. The allocation result is stored with the shipment for audit.
- **Notifications are eventual**, fired off the booking event, not blocking the response.
- **The carrier adapter is the only place that knows about courier-specific shapes.** Everything upstream/downstream uses the canonical model.
- **Audit observes every step.**

## How channels integrate (orders in)

The mirror of carriers — channels also use an adapter pattern.

```mermaid
flowchart LR
    SH[Shopify webhook] --> ADP_SH[Shopify adapter]
    AM[Amazon SQS event] --> ADP_AM[Amazon adapter]
    WC[WooCommerce webhook] --> ADP_WC[WC adapter]
    POL[Poll: Meesho/etc] --> ADP_PL[Polling worker]
    CSV[CSV upload] --> ADP_CSV[CSV adapter]
    MAN[Manual form] --> ADP_M[Manual ingest]

    ADP_SH & ADP_AM & ADP_WC & ADP_PL & ADP_CSV & ADP_M --> CAN[Canonical Order Builder]
    CAN --> DED[Idempotency / dedup]
    DED --> OMS[OMS]
```

Detailed in [`07-integrations/01-channel-adapter-framework.md`](../07-integrations/01-channel-adapter-framework.md).

## How carriers integrate (booking + tracking out)

```mermaid
flowchart LR
    OMS[Shipment booking] --> ROUTE[Carrier router]
    ROUTE --> ADP1[Delhivery adapter]
    ROUTE --> ADP2[Bluedart adapter]
    ROUTE --> ADP3[Ekart adapter]
    ROUTE --> ADPN[... N more adapters]

    ADP1 & ADP2 & ADP3 & ADPN --> CRR_API[Courier APIs]

    CRR_API -- webhooks --> WHK[Webhook receiver]
    CRR_API -- polled --> POLLER[Polling worker]
    WHK & POLLER --> NORM[Status normalizer]
    NORM --> TRACK[Tracking ctx]
```

Detailed in [`07-integrations/02-courier-adapter-framework.md`](../07-integrations/02-courier-adapter-framework.md).

> **First-party note (v3+, if at all):** if Pikshipp ever builds its own first-party last-mile network, it plugs in here as just another adapter. There is no special path. The architecture treats it identically to a third-party carrier. We do not build a fleet for v1 or v2.

## Identity & seller-config as the spine

Every diagram in this PRD has identity and seller-scoping as an invisible cross-cutting layer. To make it visible:

```mermaid
flowchart TB
    subgraph IDENT["Identity & seller-config"]
        SLR[Sellers]
        USR[Users]
        RLS[Roles + Permissions]
        SES[Sessions]
        CFG[Seller config vector]
        AUD[Audit log]
    end
    REQ[Every request] --> AUTHN[Authenticate]
    AUTHN --> AUTHZ[Authorize + seller scope]
    AUTHZ --> POLR[Policy engine resolves applicable config]
    POLR --> CTX[Domain context]
    CTX --> AUD
```

The identity layer **MUST** intercept every request before any domain context sees it. There is no "internal" path that bypasses scope. The policy engine resolves applicable seller-level configuration once per request (or cached) and downstream contexts read from it. Detailed in [`02-multi-tenancy-model.md`](./02-multi-tenancy-model.md) (now "Seller config & data scoping") and [`05-policy-engine.md`](./05-policy-engine.md).

## What is *not* in this overview

- Storage choices (SQL vs NoSQL) — HLD.
- Service decomposition (monolith vs microservices) — HLD.
- Programming language / frameworks — HLD.
- Cloud provider, regions, availability — HLD.
- Cost model — HLD + finance.

The PRD is deliberately silent on these so we don't pre-commit before the engineering team weighs in.

## Open architectural questions

Logged in [`09-appendix/02-open-questions.md`](../09-appendix/02-open-questions.md). Notable ones:

- **Q-A1** — Are reconciliation (weight, COD, courier invoice) one bounded context or three?
- **Q-A2** — Is the catalog context required at v1?
- **Q-A3** — Does the allocation engine call the pricing engine, or vice versa? (Currently: allocation calls pricing.)
- **Q-A4** — How tightly coupled is the channel adapter to the order context's schema?
