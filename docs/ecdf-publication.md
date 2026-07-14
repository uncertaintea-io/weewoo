# ECDF publication deployment contract

Generated joint ECDFs are stored in PostgreSQL. Every publisher and reader must
use the same database. No shared filesystem or ECDF output directory is needed.

The `ecdf` table retains the five newest versions for each
`(service_id, indicator_id)` pair. A publication computes and stores the byte
length and SHA-256 checksum with the binary body. Insertion of the new version
and deletion of versions outside the retention window happen in one database
transaction, so readers continue to see the previous committed version until
the new one is complete. Reads verify the stored length and SHA-256 checksum
and fall back through retained versions if a newer row fails verification.

Publishers coordinate with a PostgreSQL advisory lock keyed by service and
indicator. If another process already holds that lock, the scheduled invocation
is skipped successfully. This permits multiple server instances to run the
scheduler without generating the same ECDF concurrently.

Each publication also records the scheduler's aligned `interval_end`. After a
publisher acquires the advisory lock, it skips the build if that service and
indicator already have a version for the same interval. A unique database index
provides a final safeguard that at most one version is stored per interval.

Configuration:

- `ecdf_scheduled_build_timeout`: complete scheduled invocation timeout;
  defaults to `5m`.
