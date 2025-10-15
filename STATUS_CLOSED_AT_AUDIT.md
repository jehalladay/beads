# Status/Closed_At Consistency Audit

**Date**: 2025-10-15
**Issue**: bd-227
**Database**: .beads/bd.db

---

## Summary

**Total issues**: 244
**Inconsistent issues**: 86 (35.2%)
**Consistent issues**: 158 (64.8%)

### Breakdown

- **Closed issues missing closed_at**: 86 (92% of closed issues)
- **Non-closed issues with closed_at**: 0
- **Total closed issues**: 93

---

## Findings

### 1. Missing closed_at for Closed Issues (86 issues)

All inconsistencies are `status='closed'` but `closed_at IS NULL`. No cases of the reverse (non-closed with a timestamp).

**Sample of affected issues**:
```
bd-1, bd-2, bd-5, bd-9, bd-10, bd-11, bd-12, bd-13, bd-14, bd-15, bd-16, bd-17, bd-18, bd-19, 
bd-20, bd-21, bd-22, bd-23, bd-24, bd-25, bd-27, bd-31, bd-32, bd-33, bd-34, bd-35, bd-36, 
bd-38, bd-39, bd-42, bd-43, bd-44, bd-45, bd-46, bd-47, bd-48, bd-49, bd-50, bd-51, bd-52, 
bd-53, bd-54, bd-55, bd-56, bd-57, bd-58, bd-59, bd-60, bd-61, bd-62, bd-64, bd-65, bd-66, 
bd-67, bd-69, bd-70, bd-83, bd-85, bd-91, bd-93, bd-167, bd-168, bd-170, bd-171, bd-172, 
bd-173, bd-174, bd-175, bd-176, bd-177, bd-178, bd-179, bd-182, bd-196, bd-197
... and 10 test-* issues
```

---

## Root Cause Analysis

The inconsistency pattern suggests:

1. **Historical bug**: Issues were closed before `closed_at` column was properly enforced
2. **Pattern**: All old issues (bd-1 through bd-93) are affected when closed
3. **Recent issues**: Issues bd-200+ appear to have proper closed_at timestamps

This is a **data migration issue**, not an ongoing bug in the current code.

---

## Cleanup Strategy

### Recommended Approach: Trust Status, Set closed_at

Since `status='closed'` is the authoritative field and `closed_at` should reflect when it was closed:

1. **For issues with status='closed' and closed_at IS NULL**:
   - Set `closed_at = updated_at` (best approximation)
   - This preserves the status as truth
   - Provides a reasonable timestamp

2. **Why trust status?**:
   - Status is user-visible and actively used
   - closed_at is secondary metadata
   - All user commands operate on status
   - Reopening an issue would break if we changed status to match closed_at=NULL

---

## Cleanup SQL Script

```sql
-- Fix closed issues missing closed_at timestamp
-- Set closed_at to updated_at as best approximation
UPDATE issues 
SET closed_at = updated_at 
WHERE status = 'closed' 
  AND closed_at IS NULL;

-- Verify the fix
SELECT COUNT(*) as remaining_inconsistencies
FROM issues 
WHERE (status = 'closed' AND closed_at IS NULL) 
   OR (status != 'closed' AND closed_at IS NOT NULL);
```

**Expected result**: 0 remaining inconsistencies

---

## Verification Queries

### Before cleanup:
```sql
SELECT COUNT(*) FROM issues WHERE status = 'closed' AND closed_at IS NULL;
-- Expected: 86
```

### After cleanup:
```sql
SELECT COUNT(*) FROM issues WHERE status = 'closed' AND closed_at IS NULL;
-- Expected: 0

SELECT COUNT(*) FROM issues WHERE status = 'closed' AND closed_at IS NOT NULL;
-- Expected: 93
```

---

## Migration Plan

1. **Backup first**: `cp .beads/bd.db .beads/bd.db.backup-$(date +%s)`
2. **Run cleanup SQL**: Apply the UPDATE statement
3. **Verify**: Check that all inconsistencies are resolved
4. **Add constraint**: Proceed with schema migration (next issue)

---

## Next Steps

After this cleanup:
- Apply CHECK constraint to prevent future inconsistencies
- Update code to enforce invariant in all write paths
- Add tests for constraint enforcement

**Unblocks**: Schema migration work for status/closed_at invariant
