-- beads-ijzkb: persist compound-issue lineage (types.Issue.BondedFrom).
-- `mol bond` populates BondedFrom in memory but it was never persisted, so
-- IsCompound() always returned false and `mol show` never rendered Compound.
-- bonded_from stores a JSON array of BondRef ({source_id,bond_type,bond_point})
-- as TEXT, mirroring the `waiters` JSON-TEXT column convention.
--
-- Numbered 0055 (NOT 0054) deliberately: the fleet hub carries phantom v54
-- lease columns from an unmerged 0054_add_lease_columns branch. The migration
-- selector is MAX(version)-based with no contiguity assertion, so a real 0054
-- would be silently filtered out on any DB that recorded a 54 row. 0055 applies
-- cleanly under every v54 outcome (rollback, roll-forward, or status quo).
SET @needs_add = (
    SELECT IF(COUNT(*) = 0, 1, 0)
    FROM INFORMATION_SCHEMA.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'issues'
      AND COLUMN_NAME = 'bonded_from'
);
SET @sql = IF(@needs_add = 1,
    'ALTER TABLE issues ADD COLUMN bonded_from TEXT DEFAULT ''''',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @needs_add = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisps') > 0
    AND
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
        WHERE TABLE_SCHEMA = DATABASE()
          AND TABLE_NAME = 'wisps'
          AND COLUMN_NAME = 'bonded_from') = 0,
    1, 0
);
SET @sql = IF(@needs_add = 1,
    'ALTER TABLE wisps ADD COLUMN bonded_from TEXT DEFAULT ''''',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
