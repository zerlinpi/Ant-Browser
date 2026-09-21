#!/bin/sh
set -eu

: "${CONTROL_PLANE_DB_PASSWORD:?CONTROL_PLANE_DB_PASSWORD is required}"
: "${WORKER_DB_PASSWORD:?WORKER_DB_PASSWORD is required}"

# Runs only for a fresh PostgreSQL data directory. Schema ownership stays with
# POSTGRES_USER; services use non-owner roles so FORCE RLS is effective.
psql --set=ON_ERROR_STOP=1 \
  --username "$POSTGRES_USER" \
  --dbname "$POSTGRES_DB" \
  --set=control_password="$CONTROL_PLANE_DB_PASSWORD" \
  --set=worker_password="$WORKER_DB_PASSWORD" <<-'SQL'
SELECT format(
  'CREATE ROLE ant_control_plane LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS PASSWORD %L',
  :'control_password'
) WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ant_control_plane') \gexec

SELECT format(
  'CREATE ROLE ant_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS PASSWORD %L',
  :'worker_password'
) WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ant_worker') \gexec
SQL
