# ECDF publication deployment contract

Generated joint ECDFs are stored in the configured database. Every publisher and reader must
use the same database. No shared filesystem or ECDF output directory is needed.

The `ecdf` table retains the five newest versions for each
`(service_id, indicator_id)` pair. A publication computes and stores the byte
length and SHA-256 checksum with the binary body. Insertion of the new version
and deletion of versions outside the retention window happen in one database
transaction, so readers continue to see the previous committed version until
the new one is complete. Reads verify the stored length and SHA-256 checksum
and fall back through retained versions if a newer row fails verification.

PostgreSQL publishers coordinate with an advisory lock keyed by service and
indicator. If another process already holds that lock, the scheduled invocation
is skipped successfully. SQLite servers serialize database work through one
connection and each server owns its database file. PostgreSQL therefore remains
the backend for multiple server processes sharing one database.

Each publication also records the scheduler's aligned `interval_end`. After a
publisher acquires the advisory lock, it skips the build if that service and
indicator already have a version for the same interval. A unique database index
provides a final safeguard that at most one version is stored per interval.
Publications use the eligible chunks available at build time and are not
deferred by pending collection recovery. Subsequent builds incorporate
recovered eligible chunks.

The hourly publisher also builds **Load vs. UTC Time of Day** under indicator
ID 2. Its first publication waits until eligible chunks cover at least 95% of
the expected chunks in its five-day Training range. It uses all eligible chunks
in the active service generation. See
[the time-of-day design](design/load-time-of-day.md).

Configuration:

- `ecdf_scheduled_build_timeout`: complete scheduled invocation timeout;
  defaults to `5m`.
