DROP TRIGGER IF EXISTS jobs_append_terminal_notification ON jobs;
DROP FUNCTION IF EXISTS append_terminal_job_notification();
DROP TABLE IF EXISTS notifications;
DROP FUNCTION IF EXISTS reject_notification_update();
