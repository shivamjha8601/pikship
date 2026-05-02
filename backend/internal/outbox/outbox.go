// Package outbox implements the transactional outbox pattern.
//
// Domain services write an outbox_event row inside their own transaction
// (via Emitter.Emit). A background Forwarder scans pending rows, enqueues
// a River job per row, and marks enqueued_at. The River job handler then
// dispatches each event to the registered consumer.
//
// Per LLD §03-services/03-outbox.
package outbox
