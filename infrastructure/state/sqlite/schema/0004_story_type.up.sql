-- Migration 0004 — story_type column on stories.
--
-- Drives the stage-applicability matrix: doc stories skip atdd /
-- automate / test-review automatically; code stories run the full
-- STRICT pipeline; mixed runs everything. Default 'code' so existing
-- rows + epics.md entries without explicit story_type behave as
-- before (run the full pipeline) — explicit opt-in for doc to save
-- subagent tokens.

ALTER TABLE stories ADD COLUMN story_type TEXT NOT NULL DEFAULT 'code';
