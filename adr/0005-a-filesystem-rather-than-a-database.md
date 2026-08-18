# A filesystem rather than a database

* Status: accepted
* Date: 2026-08-18

## Context and problem statement

Fledge holds builds, the metadata read out of each one, and enrolled devices.
Realistic volumes are a handful of apps, tens of builds, and fewer than a hundred devices, because Apple's own limit caps the last one.

The archives themselves have to sit on a filesystem or in object storage regardless, since they are megabytes each and get streamed to devices with range requests.
So the question is only where the metadata goes.

## Considered options

### JSON sidecars on the same filesystem as the archives

One directory per build, holding the archive, its extracted icon, and a `build.json`.
One file per device.

* Good, because the storage directory explains itself. Anyone can look and see what is published without Fledge running.
* Good, because there is one thing to back up and one thing to restore.
* Good, because a build is content addressed by its archive's digest, which makes uploading the same archive twice idempotent for free.
* Good, because it deploys as one container and one volume, with no database to run, migrate or upgrade.
* Bad, because listing means reading every sidecar, which is fine at tens of builds and would not be at tens of thousands.
* Bad, because concurrent writes need handling in the process rather than by a transaction.
* Bad, because there are no queries beyond what the code walks itself.

### Postgres

* Good, because transactions, indexes and real queries.
* Good, because it matches how other services here are already deployed.
* Bad, because the archives still live outside it, so there are now two stores that can disagree, and a restore has to reconcile them.
* Bad, because it is a database, a migration story and a backup for records that would fit in a text file.

### SQLite beside the archives

* Good, because transactions without a server.
* Good, because one volume still holds everything.
* Bad, because it trades a directory anyone can read for a file needing a tool, and buys transactions this workload does not need.

## Decision outcome

JSON sidecars on the filesystem.

Writes go through a mutex and land atomically: content is written to a temporary file in the destination directory and renamed into place, so a reader never sees a partial sidecar and a crash never leaves a truncated one.
Uploads stage into the store's own directory first, so the rename into place cannot cross a filesystem boundary.

The bundle identifier is the one untrusted value that becomes a path component, since it is read out of an uploaded archive.
It is validated against a strict pattern and rejected otherwise.

The trigger for revisiting this is listing latency, not record count in the abstract.
When walking the tree to render the index becomes noticeable, an index file or an embedded database becomes worth it.
Nothing about the layout makes that migration hard, because the sidecars are the data.
