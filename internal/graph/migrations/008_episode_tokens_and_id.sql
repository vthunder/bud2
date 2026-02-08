-- Add token count and short ID to episodes table
ALTER TABLE episodes ADD COLUMN token_count INTEGER DEFAULT 0;
ALTER TABLE episodes ADD COLUMN short_id TEXT DEFAULT '';

-- Create index on short_id for quick lookups
CREATE INDEX IF NOT EXISTS idx_episodes_short_id ON episodes(short_id);

-- Remove level 0 (full text) from episode_summaries
-- We'll fall back to episodes.content for full text
DELETE FROM episode_summaries WHERE compression_level = 0;
