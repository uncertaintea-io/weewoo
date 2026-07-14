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

Configuration:

- `ecdf_scheduled_build_timeout`: complete scheduled invocation timeout;
  defaults to `5m`.
- `ECDF_PUBLISHER_ENABLED`: defaults to `true`. Setting it to `false` disables
  scheduled publication on that instance; committed database reads remain
  available.
