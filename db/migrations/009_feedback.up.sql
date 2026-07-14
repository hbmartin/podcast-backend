-- Feedback reports from the app (the support flow and shake-to-report in
-- TestFlight builds). user_id is null for anonymous submissions; diagnostics
-- columns mirror the SupportFeedbackRequest proto extension.
CREATE TABLE feedback (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    message TEXT NOT NULL,
    subject TEXT NOT NULL DEFAULT '',
    inbox TEXT NOT NULL DEFAULT '',
    logs TEXT NOT NULL DEFAULT '',
    bitdrift_session_id TEXT NOT NULL DEFAULT '',
    device_info TEXT NOT NULL DEFAULT '',
    app_version TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
