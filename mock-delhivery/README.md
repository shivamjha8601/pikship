# mock-delhivery

Tiny in-memory mock of the Delhivery Express API for local development.
Throwaway — not part of the main backend module.

## Run

```sh
go run . -addr :8088
```

Then point the backend's Delhivery adapter at it:

```sh
export PIKSHIPP_DELHIVERY_BASE_URL=http://localhost:8088
```

## Endpoints

| Method | Path                                              | Purpose             |
|--------|---------------------------------------------------|---------------------|
| GET    | `/c/api/pin-codes/json/?filter_codes=<pin>`       | Serviceability      |
| POST   | `/api/cmu/create.json` (form: `format`, `data`)   | Create shipment     |
| POST   | `/api/p/edit` (form: `data` with `cancellation`)  | Cancel shipment     |
| GET    | `/api/v1/packages/json/?waybill=<awb>&verbose=2`  | Tracking            |
| GET    | `/api/p/packing_slip?wbns=<awb>&pdf=true`         | Label (PDF stub)    |

## Status auto-progression

Once a shipment is created, its status walks forward with age:

| Age    | Status            |
|--------|-------------------|
| 0–30s  | Manifested        |
| 30–60s | In Transit        |
| 60–90s | Out for Delivery  |
| 90s+   | Delivered         |

Cancelled shipments stop progressing.
