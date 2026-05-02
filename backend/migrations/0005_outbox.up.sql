-- 0005_outbox.up.sql
-- Per LLD §03-services/03-outbox: domain tx writes outbox_event rows;
-- the forwarder claims pending rows with FOR UPDATE SKIP LOCKED, enqueues
-- a river job, then UPDATEs enqueued_at.

CREATE TABLE outbox_event (
    id            UUID PRIMARY KEY,
    seller_id     UUID,
    kind          TEXT NOT NULL,
    version       INT  NOT NULL DEFAULT 1,
    payload       JSONB NOT NULL DEFAULT '{}',
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    enqueued_at   TIMESTAMPTZ
);

-- Forwarder hot path: scan pending rows by created_at.
CREATE INDEX outbox_event_pending_idx ON outbox_event (created_at)
    WHERE enqueued_at IS NULL;

-- Per-seller listing for debugging and audit replay.
CREATE INDEX outbox_event_seller_idx ON outbox_event (seller_id, created_at)
    WHERE enqueued_at IS NOT NULL;

-- No RLS: outbox is a platform-internal table written inside the same tx
-- as the domain change, so seller scoping is enforced by the calling code.
GRANT SELECT, INSERT, UPDATE, DELETE ON outbox_event TO pikshipp_app;
GRANT SELECT                         ON outbox_event TO pikshipp_admin;
