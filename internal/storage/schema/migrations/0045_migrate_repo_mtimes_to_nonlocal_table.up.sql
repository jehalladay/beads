SET @repo_mtimes_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
                           WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'repo_mtimes') > 0;
SET @sql = IF(@repo_mtimes_exists,
    'DELETE FROM dolt_ignore WHERE pattern = ''repo_mtimes''',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @sql = IF(@repo_mtimes_exists,
    'CALL DOLT_COMMIT(''-Am'', ''remove repo_mtimes from dolt_ignore'')',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
INSERT INTO dolt_nonlocal_tables (table_name, target_ref, options) VALUES ('repo_mtimes', 'main', 'immediate');
CALL DOLT_COMMIT('-Am', 'create nonlocal table repo_mtimes');
