SET @wisp_labels_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
                           WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisp_labels') > 0;
SET @sql = IF(@wisp_labels_exists,
    'DELETE FROM dolt_ignore WHERE pattern = ''wisp_labels''',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @sql = IF(@wisp_labels_exists,
    'CALL DOLT_COMMIT(''-Am'', ''remove wisp_labels from dolt_ignore'')',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
INSERT INTO dolt_nonlocal_tables (table_name, target_ref, options) VALUES ('wisp_labels', 'main', 'immediate');
CALL DOLT_COMMIT('-Am', 'create nonlocal table wisp_labels');
