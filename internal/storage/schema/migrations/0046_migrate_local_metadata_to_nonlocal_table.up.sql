SET @local_metadata_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
                              WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'local_metadata') > 0;
SET @sql = IF(@local_metadata_exists,
    'DELETE FROM dolt_ignore WHERE pattern = ''local_metadata''',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @sql = IF(@local_metadata_exists,
    'CALL DOLT_COMMIT(''-Am'', ''remove local_metadata from dolt_ignore'')',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
INSERT INTO dolt_nonlocal_tables (table_name, target_ref, options) VALUES ('local_metadata', 'main', 'immediate');
CALL DOLT_COMMIT('-Am', 'create nonlocal table local_metadata');
