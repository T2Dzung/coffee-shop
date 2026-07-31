# PROD RDS point-in-time restore drill

This runbook exercises the automated-backup path of the current PROD RDS PostgreSQL
database. It restores a new private target at a selected point in time, validates data
through the application database role, and then deletes only that target.

The drill is intentionally isolated:

- it does not overwrite the source database;
- it does not update the application endpoint or Secret;
- it does not send traffic to the restored target;
- it does not claim multi-Region disaster recovery or an RTO/RPO commitment.

## Preconditions

Run the drill only when:

1. the intended AWS account and Region pass `config doctor`;
2. PROD setup and runtime status are healthy;
3. the source RDS automated backup and restorable window are active;
4. the promoted migration image is pinned by digest;
5. no other resource tagged `Purpose=restore-drill` exists;
6. you can remain available until the temporary RDS target is cleaned up.

The target incurs RDS and storage charges until cleanup completes.

```bash
bash scripts/platformctl.sh config doctor --environment prod
bash scripts/platformctl.sh prod status
```

## Create a private state path

The state contains resource identities, timestamps and checksums. Keep it outside Git.
The CLI writes it atomically with mode `0600`.

```bash
mkdir -p "$HOME/coffeeshop-evidence"
chmod 700 "$HOME/coffeeshop-evidence"
STATE="$HOME/coffeeshop-evidence/prod-pitr-$(date -u +%Y%m%dT%H%M%SZ).json"
```

Keep the printed `State:` path if you do not set one explicitly. Resume and cleanup must
use the same file.

## Run the restore and validation

```bash
bash scripts/platformctl.sh prod restore-drill run --state "$STATE"
```

The command:

1. verifies the source identity, backup window, network and application TLS credential;
2. writes marker A and persists a timestamp after its commit;
3. waits until that timestamp enters the RDS restorable window;
4. writes marker B;
5. displays the exact source, target, account, Region and target policy;
6. waits for the literal `restore` approval;
7. restores a private, Single-AZ target with backup retention disabled;
8. validates that marker A exists, marker B does not, the checksum matches and the
   bounded application tables exist;
9. stops at `cleanup-pending`.

If the terminal or network fails, rerun the same command with the same state. The state
machine resumes from the last completed phase; it must not create a second target or
write new markers.

## Inspect without mutation

```bash
bash scripts/platformctl.sh prod restore-drill status --state "$STATE"
```

Status reads the saved identity and reports the live source/target states. An RDS target
being `available` is not enough to call the drill successful; the marker, checksum,
schema and application-role checks must also have passed.

## Clean up the exact target

After validation and any required investigation:

```bash
bash scripts/platformctl.sh prod restore-drill cleanup --state "$STATE"
```

Review the source and target identities, then type the literal `cleanup`. Cleanup:

- rechecks account, Region, source/target inequality, ARN, resource ID and ownership
  tags;
- removes the exact temporary RDS target without a final snapshot;
- removes only the drill marker and bounded Kubernetes Jobs;
- verifies that no target or tagged drill orphan remains.

Any identity or tag mismatch fails closed. Do not replace the check with a broad name
match or delete the target manually unless following a documented break-glass process.

## Failure handling

| Symptom | Action |
| --- | --- |
| Backup window is empty | Stop; wait for the configured automated backup to become restorable |
| Source TLS probe fails | Check the application Secret, DNS, security group and database role without printing credentials |
| Process exits after restore request | Resume with the same state file; do not submit another restore |
| Target is available but validation fails | Keep it for investigation; do not auto-delete the best recovery evidence |
| Marker A missing, marker B present or checksum differs | Treat the drill as failed and inspect the selected timestamp and target identity |
| Target identity or tags differ during cleanup | Stop; establish ownership before deleting anything |

One observed restore duration is a timing for that run, not an RTO. A real recovery
objective requires a business target, repeated measurements and an end-to-end cutover
scope.
