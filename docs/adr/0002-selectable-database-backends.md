# Support selectable PostgreSQL and SQLite database backends

WeeWoo server operators may select `postgresql` or `sqlite` with the
`database` setting. Each server uses exactly one backend. PostgreSQL supports
deployments that coordinate multiple WeeWoo processes through database locks;
an SQLite-backed server owns one local database file and serializes access
through one connection.

The application continues to use `database/sql` and the existing persistence
interfaces. Backend-specific behavior is limited to connection bootstrap,
migrations, locking, and SQL that is not portable. This avoids duplicating all
persistence methods while keeping backend selection explicit.

Inferring the backend from `connection_string` was rejected because configuration
must state the operator's intent. Separate PostgreSQL and SQLite store trees
were rejected because most queries and all caller-facing interfaces are shared.
