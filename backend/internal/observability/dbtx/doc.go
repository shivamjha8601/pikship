// Package dbtx wraps pgxpool with the conventions every Pikshipp domain
// service depends on:
//
//   - Three connection roles (App / Reports / Admin) with distinct pools.
//   - Transaction helpers that automatically SET LOCAL app.seller_id so RLS
//     scopes the work to one seller.
//   - A retryable-error classifier for the small set of pg conditions that
//     callers may safely retry.
//
// Per LLD §02-infrastructure/01-database-access. Domain code MUST NOT call
// pool.Begin() directly — always go through one of the With*Tx helpers.
package dbtx
