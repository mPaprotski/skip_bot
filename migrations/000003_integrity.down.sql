DROP TABLE webhook_updates;
DROP INDEX idx_schedule_date_unique;
DROP INDEX idx_schedule_weekly_unique;
DROP INDEX idx_dialog_one_per_user;
ALTER TABLE absences DROP COLUMN start_at;
UPDATE absences SET absence_date=strftime('%Y-%m-%d',absence_date,'unixepoch');
UPDATE schedule SET specific_date=strftime('%Y-%m-%d',specific_date,'unixepoch') WHERE specific_date IS NOT NULL;
