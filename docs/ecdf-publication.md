# ECDF publication deployment contract

The currently supported publication topology is one active ECDF publisher on
one computer, or multiple local processes that all access the same underlying
local filesystem. Many clients and read-only server instances may use the
service without changing this invariant.

ECDF writers coordinate through a persistent `joint-ecdf.lock.json` file and
the filesystem's kernel `flock` implementation. Every local writer must resolve
the same `ecdf_output_dir` and open the same underlying lock file. The backing
filesystem must provide reliable `flock` behavior.

The lock file intentionally remains on disk. Its presence does not mean the
lock is held. It must never be deleted, renamed, replaced, rotated, or cleaned
up while any application instance may be running. File age and PID metadata
must never be used to break or steal the kernel lock.

Local `flock` does not coordinate machines, pods, or containers with independent
filesystems. Until a distributed publication architecture is implemented,
operators deploying multiple server instances may set
`ECDF_PUBLISHER_ENABLED=false` on every instance except one designated
publisher. This is a transition strategy, not high availability or leader
election.

Runtime configuration keys:

- `ecdf_output_dir`: ECDF artifact and lock root.
- `ecdf_manifest_lock_wait_timeout`: maximum lock wait; defaults to `1m`.
- `ecdf_scheduled_build_timeout`: complete scheduled invocation timeout;
  defaults to `5m`.

`ECDF_PUBLISHER_ENABLED` defaults to `true`. Setting it to `false` disables only
scheduled publication; committed ECDF reads remain available.

Multi-node publication requires a separately reviewed coordination and shared
artifact-storage architecture.
