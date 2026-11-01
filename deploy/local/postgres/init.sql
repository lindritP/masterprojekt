-- Runs once, when the local PostgreSQL volume is first initialised.
-- Creates the application role the migrations grant privileges to (FR-030).
-- Local test credentials only (DR-008).
CREATE ROLE ccsra_app LOGIN PASSWORD 'ccsra-app-local';
