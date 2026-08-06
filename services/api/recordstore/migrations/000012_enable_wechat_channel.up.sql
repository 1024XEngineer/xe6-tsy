-- Allow verified WeChat Work destinations and outbound messages on the wechat channel.
ALTER TABLE account_destinations
    DROP CONSTRAINT account_destinations_channel_valid;

ALTER TABLE account_destinations
    ADD CONSTRAINT account_destinations_channel_valid
    CHECK (channel IN ('email', 'wechat'));

ALTER TABLE message_preferences
    DROP CONSTRAINT message_preferences_channel_valid;

ALTER TABLE message_preferences
    ADD CONSTRAINT message_preferences_channel_valid
    CHECK (channel IN ('email', 'wechat'));

ALTER TABLE outbound_messages
    DROP CONSTRAINT outbound_messages_channel_valid;

ALTER TABLE outbound_messages
    ADD CONSTRAINT outbound_messages_channel_valid
    CHECK (channel IN ('email', 'wechat'));
