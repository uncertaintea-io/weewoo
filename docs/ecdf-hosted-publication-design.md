# Future hosted ECDF publication design

This note describes a future direction only. It is not implemented by the
current local-`flock` publisher.

Multiple API replicas should not automatically run the ECDF scheduler. A
dedicated publisher or worker role should own scheduled builds, while API
replicas remain readers. All publishers and readers that need ECDF data require
shared, durable artifact storage; a distributed lock alone does not make
container-local files shared.

For high availability, publisher ownership should use a lease or elected
leader. Because an expired owner may resume after losing its lease, ownership
must include fencing or conditional publication. An old publisher must be
unable to commit after a newer owner takes over.

Published artifacts should be immutable and versioned. Updating the pointer to
the current version must be atomic or conditional on the expected prior state.
The last known-good pointer must remain usable when builds, publishers, or
coordination services fail.

Coordination should eventually be scoped by tenant, service, indicator, or
artifact so unrelated customers do not block each other's publication work.

Before moving expensive generation outside the commit lock, a separate design
should establish these steps and guarantees:

1. Generate and validate a uniquely named immutable candidate before acquiring
   commit ownership.
2. After ownership is acquired, re-read the current pointer and version state.
3. Select the next version only while holding valid, fenced ownership.
4. Publish candidates with conditional writes so concurrent builders cannot
   claim the same version or overwrite a newer pointer.
5. Garbage-collect abandoned unique candidates using storage lifecycle rules,
   never by treating a lease or lock file as stale.
6. Define whether a candidate built from an older manifest may still commit, or
   must restart when current state changed during generation.

Concurrency tests should prove that two builders cannot publish the same
version, a fenced-out publisher cannot commit, pointer updates are atomic,
abandoned candidates do not become visible, unrelated tenants proceed
independently, and the last known-good artifact remains readable throughout
failover.
