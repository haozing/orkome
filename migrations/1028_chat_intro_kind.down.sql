DELETE FROM chat_message WHERE kind = 'agent_intro';
ALTER TABLE chat_message DROP CONSTRAINT IF EXISTS chat_message_kind_check;
ALTER TABLE chat_message ADD CONSTRAINT chat_message_kind_check
  CHECK (kind IN ('message','no_response','system_stopped','channel_command','onboarding'));
