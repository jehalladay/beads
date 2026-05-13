SET @wisp_dependencies_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
                                 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisp_dependencies') > 0;
SET @sql = IF(@wisp_dependencies_exists,
    'DELETE FROM dolt_ignore WHERE pattern = ''wisp_dependencies''',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @sql = IF(@wisp_dependencies_exists,
    'CALL DOLT_COMMIT(''-Am'', ''remove wisp_dependencies from dolt_ignore'')',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
INSERT INTO dolt_nonlocal_tables (table_name, target_ref, options) VALUES ('wisp_dependencies', 'main', 'immediate');
CALL DOLT_COMMIT('-Am', 'create nonlocal table wisp_dependencies');
