SET @wisps_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
                     WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisps') > 0;
SET @sql = IF(@wisps_exists,
    'DELETE FROM dolt_ignore WHERE pattern = ''wisps''',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @sql = IF(@wisps_exists,
    'CALL DOLT_COMMIT(''-Am'', ''remove wisps from dolt_ignore'')',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
INSERT INTO dolt_nonlocal_tables (table_name, target_ref, options) VALUES ('wisps', 'main', 'immediate');
CALL DOLT_COMMIT('-Am', 'create nonlocal table wisps');
