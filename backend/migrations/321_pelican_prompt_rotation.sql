-- 鹈鹕测智 group tests may rotate through a list of prompts (pelican_config.prompts);
-- prompt_rotation is the persisted round-robin cursor so restarts keep the order.
ALTER TABLE pelican_group_test_plans ADD COLUMN IF NOT EXISTS prompt_rotation BIGINT NOT NULL DEFAULT 0;
