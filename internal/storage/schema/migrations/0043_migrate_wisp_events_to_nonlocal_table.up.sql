REPLACE INTO dolt_ignore VALUES ('__temp_wisp_events', true);
ALTER TABLE wisp_events RENAME TO __temp_wisp_events;
CREATE TABLE wisp_events (
    id CHAR(36) NOT NULL PRIMARY KEY DEFAULT (UUID()),
    issue_id VARCHAR(255) NOT NULL,
    event_type VARCHAR(32) NOT NULL,
    actor VARCHAR(255) DEFAULT '',
    old_value TEXT DEFAULT '',
    new_value TEXT DEFAULT '',
    comment TEXT DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_wisp_events_issue (issue_id),
    INDEX idx_wisp_events_created_at (created_at)
);
INSERT INTO dolt_nonlocal_tables (table_name, target_ref, options) VALUES ('wisp_events', 'main', 'immediate');
CALL DOLT_COMMIT('-Am', 'create nonlocal table wisp_events');
INSERT INTO wisp_events SELECT * FROM __temp_wisp_events;
DROP TABLE __temp_wisp_events;
DELETE FROM dolt_ignore WHERE pattern = '__temp_wisp_events';
