#!/usr/bin/env bash
# demo.sh — end-to-end demonstration of the storage_cli API
#
# Run after building:
#   go build -o storage_cli ./cmd/storage_cli/
#   bash demo.sh
#
# Scenario: managing an employee table from creation to deletion.
# Every CLI command is echoed before execution so the output is self-documenting.

set -euo pipefail

CLI=./storage_cli
DB=$(mktemp -d)

# ── helpers ───────────────────────────────────────────────────────────────────
step() { echo; echo "━━━ $* ━━━"; }
run()  { echo "  \$ $CLI --dir \$DB $*" >&2; "$CLI" --dir "$DB" "$@"; }
trap 'rm -rf "$DB"' EXIT

# ── preflight ─────────────────────────────────────────────────────────────────
if [[ ! -x "$CLI" ]]; then
    echo "storage_cli not found. Build it first:"
    echo "  go build -o storage_cli ./cmd/storage_cli/"
    exit 1
fi
echo "database directory: $DB"

# ── 1. create-table ───────────────────────────────────────────────────────────
step "1. create-table"
run create-table employees \
    id:int \
    'name:varchar(64)' \
    'department:varchar(32)' \
    salary:double \
    active:boolean

# ── 2. list-tables ────────────────────────────────────────────────────────────
step "2. list-tables"
run list-tables

# ── 3. describe ───────────────────────────────────────────────────────────────
step "3. describe employees"
run describe employees

# ── 4. insert rows ────────────────────────────────────────────────────────────
step "4. insert 5 employees"
RID1=$(run insert employees 1 Alice    Engineering 95000  true  | awk '{print $2}')
RID2=$(run insert employees 2 Bob      Marketing   72000  true  | awk '{print $2}')
RID3=$(run insert employees 3 Carol    Engineering 88000  true  | awk '{print $2}')
RID4=$(run insert employees 4 Dave     Sales       65000  true  | awk '{print $2}')
RID5=$(run insert employees 5 Eve      Engineering 102000 true  | awk '{print $2}')
echo "  assigned RIDs: $RID1 $RID2 $RID3 $RID4 $RID5"

# ── 5. scan all ───────────────────────────────────────────────────────────────
step "5. scan — all 5 employees"
run scan employees

# ── 6. get one row by RID ─────────────────────────────────────────────────────
step "6. get Alice ($RID1)"
run get employees "$RID1"

# ── 7. update — Alice gets a promotion ───────────────────────────────────────
step "7. update Alice ($RID1): promoted to Staff Engineering, salary 115000"
NEW_RID=$(run update employees "$RID1" 1 Alice "Staff Engineering" 115000 true | awk '{print $2}')
echo "  Alice's new RID after update: $NEW_RID"
echo "  (update = delete-then-reinsert; old RID $RID1 is now a tombstone)"

# ── 8. scan after update ──────────────────────────────────────────────────────
step "8. scan — Alice appears at new RID $NEW_RID"
run scan employees

# ── 9. delete — Bob resigned ──────────────────────────────────────────────────
step "9. delete Bob ($RID2)"
run delete employees "$RID2"

# ── 10. get deleted row ───────────────────────────────────────────────────────
step "10. get Bob ($RID2) after delete → not found"
run get employees "$RID2"

# ── 11. scan final state ──────────────────────────────────────────────────────
step "11. scan — 4 remaining employees (Bob's slot is a tombstone, skipped)"
run scan employees

# ── 12. persistence ───────────────────────────────────────────────────────────
step "12. persistence — data survives process restart"
echo "  (each CLI invocation opens and closes the database independently)"
run list-tables
run scan employees

# ── 13. drop-table ────────────────────────────────────────────────────────────
step "13. drop-table employees"
run drop-table employees

# ── 14. list-tables after drop ────────────────────────────────────────────────
step "14. list-tables after drop — catalog is empty"
run list-tables

echo
echo "✓  demo complete"
