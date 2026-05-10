ALTER TABLE issues ADD COLUMN spec_id VARCHAR(1024);
CREATE INDEX idx_issues_spec_id ON issues(spec_id);
