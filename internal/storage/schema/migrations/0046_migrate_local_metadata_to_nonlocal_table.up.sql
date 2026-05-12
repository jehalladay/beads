REPLACE INTO dolt_ignore VALUES ('__temp_local_metadata', true);
-- Rename only when the legacy local_metadata table is present. Legacy clones
-- may not have it in the working set since local_metadata used to be
-- dolt-ignored and didn't transfer via DOLT_CLONE.
SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'local_metadata') > 0,
    'ALTER TABLE local_metadata RENAME TO __temp_local_metadata',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
DELETE FROM dolt_ignore WHERE pattern = 'local_metadata';
CREATE TABLE local_metadata (
    `key` VARCHAR(255) PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);
INSERT INTO dolt_nonlocal_tables (table_name, target_ref, options) VALUES ('local_metadata', 'main', 'immediate');
CALL DOLT_COMMIT('-Am', 'create nonlocal table local_metadata');
SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = '__temp_local_metadata') > 0,
    'INSERT INTO local_metadata SELECT * FROM __temp_local_metadata',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
DROP TABLE IF EXISTS __temp_local_metadata;
DELETE FROM dolt_ignore WHERE pattern = '__temp_local_metadata';
