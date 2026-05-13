SET @wisp_events_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
                           WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisp_events') > 0;
SET @sql = IF(@wisp_events_exists,
    'DELETE FROM dolt_ignore WHERE pattern = ''wisp_events''',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @sql = IF(@wisp_events_exists,
    'CALL DOLT_COMMIT(''-Am'', ''remove wisp_events from dolt_ignore'')',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
INSERT INTO dolt_nonlocal_tables (table_name, target_ref, options) VALUES ('wisp_events', 'main', 'immediate');
CALL DOLT_COMMIT('-Am', 'create nonlocal table wisp_events');
