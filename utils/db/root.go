/*
Package db is the database layer for LikeSpotifyBot: Postgres via pgx/stdlib (simple protocol for pooler compatibility), optional TTL read cache,
and the DB facade used by use cases.

Composition: database.go (connection + facade), cache.go (TTL keys), materialized.go (cached row types),
assign.go (Scan helpers), deferred.go (async write queue). Call DatabaseManager.Init after utils.Env.Init so DATABASE_URL is set.

Reads: pass cacheLifetime as 0 to always query Postgres; >0 caches successful reads until TTL or any Exec.
*/
package db

// DatabaseManager opens a *Database from DATABASE_URL (ping + sql.Open).
var DatabaseManager = NewDatabaseFactory()
