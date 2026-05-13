SET @wisp_comments_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
                             WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisp_comments') > 0;
SET @sql = IF(@wisp_comments_exists,
    'DELETE FROM dolt_ignore WHERE pattern = ''wisp_comments''',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @sql = IF(@wisp_comments_exists,
    'CALL DOLT_COMMIT(''-Am'', ''remove wisp_comments from dolt_ignore'')',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
INSERT INTO dolt_nonlocal_tables (table_name, target_ref, options) VALUES ('wisp_comments', 'main', 'immediate');
CALL DOLT_COMMIT('-Am', 'create nonlocal table wisp_comments');
SET @wildcards_exist = (SELECT COUNT(*) FROM dolt_ignore WHERE pattern IN ('wisps_%', 'wisp_%')) > 0;
SET @sql = IF(@wildcards_exist,
    'DELETE FROM dolt_ignore WHERE pattern IN (''wisps_%'', ''wisp_%'')',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @sql = IF(@wildcards_exist,
    'CALL DOLT_COMMIT(''-Am'', ''remove legacy wildcard wisp ignore entries'')',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
