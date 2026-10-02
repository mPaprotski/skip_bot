-- Rebuild date columns with INTEGER affinity and enforce schedule validation.
CREATE TABLE schedule_v3 (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 group_id INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
 subject_id INTEGER NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
 day_of_week INTEGER CHECK(day_of_week BETWEEN 0 AND 6),
 specific_date INTEGER,
 pair_number INTEGER NOT NULL CHECK(pair_number BETWEEN 1 AND 8),
 start_time TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 CHECK((day_of_week IS NULL) != (specific_date IS NULL))
);
INSERT INTO schedule_v3 SELECT id,group_id,subject_id,day_of_week,
 CASE WHEN specific_date IS NULL THEN NULL WHEN typeof(specific_date)='integer' THEN specific_date ELSE CAST(strftime('%s',specific_date) AS INTEGER) END,
 pair_number,start_time,created_at,updated_at FROM schedule;
CREATE TABLE absences_v3 (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 student_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 subject_id INTEGER NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
 schedule_id INTEGER REFERENCES schedule_v3(id) ON DELETE SET NULL,
 absence_date INTEGER NOT NULL,
 pair_number INTEGER NOT NULL,
 reason_type TEXT NOT NULL CHECK(reason_type IN ('valid','invalid')),
 comment TEXT,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 start_at INTEGER NOT NULL DEFAULT 0,
 UNIQUE(student_id,subject_id,absence_date,pair_number)
);
-- Existing marks are preserved and locked; their original deadline was not stored.
INSERT INTO absences_v3 SELECT id,student_id,subject_id,schedule_id,
 CASE WHEN typeof(absence_date)='integer' THEN absence_date ELSE CAST(strftime('%s',absence_date) AS INTEGER) END,
 pair_number,reason_type,comment,created_at,updated_at,0 FROM absences;
DROP TABLE absences;
DROP TABLE schedule;
ALTER TABLE schedule_v3 RENAME TO schedule;
ALTER TABLE absences_v3 RENAME TO absences;
CREATE INDEX idx_schedule_group_id ON schedule(group_id);
CREATE INDEX idx_schedule_subject_id ON schedule(subject_id);
CREATE INDEX idx_absences_student_id ON absences(student_id);
CREATE INDEX idx_absences_subject_id ON absences(subject_id);
CREATE INDEX idx_absences_date ON absences(absence_date);
DELETE FROM dialog_states WHERE id NOT IN (SELECT MAX(id) FROM dialog_states GROUP BY user_id);
CREATE UNIQUE INDEX idx_dialog_one_per_user ON dialog_states(user_id);
CREATE UNIQUE INDEX idx_schedule_weekly_unique ON schedule(group_id,subject_id,day_of_week,pair_number) WHERE specific_date IS NULL;
CREATE UNIQUE INDEX idx_schedule_date_unique ON schedule(group_id,subject_id,specific_date,pair_number) WHERE specific_date IS NOT NULL;
CREATE TABLE webhook_updates(update_id INTEGER PRIMARY KEY,payload TEXT NOT NULL,received_at INTEGER NOT NULL,processed_at INTEGER);
