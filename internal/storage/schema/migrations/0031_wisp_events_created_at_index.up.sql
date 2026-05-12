-- Only create the index if wisp_events is present. Legacy clones may not have
-- it in the working set since wisp_events used to be dolt-ignored and didn't
-- transfer via DOLT_CLONE.
SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisp_events') > 0,
    'CREATE INDEX IF NOT EXISTS idx_wisp_events_created_at ON wisp_events (created_at)',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
