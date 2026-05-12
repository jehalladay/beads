-- issues.started_at
SET @needs_add = (
    SELECT IF(COUNT(*) = 0, 1, 0)
    FROM INFORMATION_SCHEMA.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'issues'
      AND COLUMN_NAME = 'started_at'
);
SET @sql = IF(@needs_add = 1,
    'ALTER TABLE issues ADD COLUMN started_at DATETIME',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- wisps.started_at — only fires if the wisps table exists and lacks the column.
-- Pre-nonlocal-migration clones may not have wisps in the working set since it
-- used to be a dolt-ignored table that didn't transfer via DOLT_CLONE.
SET @needs_add = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisps') > 0
    AND
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
        WHERE TABLE_SCHEMA = DATABASE()
          AND TABLE_NAME = 'wisps'
          AND COLUMN_NAME = 'started_at') = 0,
    1, 0
);
SET @sql = IF(@needs_add = 1,
    'ALTER TABLE wisps ADD COLUMN started_at DATETIME',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
