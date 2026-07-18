DROP TRIGGER IF EXISTS webhook_endpoint_cancel_stale_deliveries ON webhook_endpoints;
DROP FUNCTION IF EXISTS cancel_stale_webhook_deliveries();
DROP TRIGGER IF EXISTS notifications_enqueue_webhooks ON notifications;
DROP FUNCTION IF EXISTS enqueue_notification_webhooks();
DROP TRIGGER IF EXISTS webhook_delivery_projection_guard ON webhook_deliveries;
DROP FUNCTION IF EXISTS guard_webhook_delivery_projection();
DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS webhook_endpoints;
