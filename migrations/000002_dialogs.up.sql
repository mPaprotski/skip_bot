-- Состояния диалогов и черновики

CREATE TABLE IF NOT EXISTS dialog_states (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    dialog_type TEXT NOT NULL,
    current_step INTEGER NOT NULL DEFAULT 0,
    data TEXT NOT NULL DEFAULT '{}', -- JSON с данными диалога
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(user_id, dialog_type)
);

CREATE INDEX IF NOT EXISTS idx_dialog_states_user_id ON dialog_states(user_id);
