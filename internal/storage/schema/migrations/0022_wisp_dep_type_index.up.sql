-- Only create the indexes if wisp_dependencies is present. Legacy clones may
-- not have it in the working set since wisp_dependencies used to be
-- dolt-ignored and didn't transfer via DOLT_CLONE.
SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisp_dependencies') > 0,
    'CREATE INDEX IF NOT EXISTS idx_wisp_dep_type ON wisp_dependencies (type)',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisp_dependencies') > 0,
    'CREATE INDEX IF NOT EXISTS idx_wisp_dep_type_depends ON wisp_dependencies (type, depends_on_id)',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
