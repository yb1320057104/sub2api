"""Patroni post_init: create a dedicated app role and database on the fresh leader."""
import json
import os
from pathlib import Path

import psycopg
from psycopg import sql


def main():
    secrets = json.loads(Path('/config/bootstrap-secrets.json').read_text())
    with psycopg.connect(host=os.environ['PATRONI_POSTGRESQL_CONNECT_ADDRESS'].rsplit(':', 1)[0],
                         port=5432, dbname='postgres', user='postgres',
                         password=secrets['pg_superuser_password'], autocommit=True) as connection:
        exists = connection.execute("SELECT 1 FROM pg_roles WHERE rolname='sub2api_app'").fetchone()
        if not exists:
            connection.execute(sql.SQL('CREATE ROLE sub2api_app LOGIN PASSWORD {} CONNECTION LIMIT 90').format(
                sql.Literal(secrets['pg_app_password'])))
        exists = connection.execute("SELECT 1 FROM pg_database WHERE datname='sub2api'").fetchone()
        if not exists:
            connection.execute('CREATE DATABASE sub2api OWNER sub2api_app')


if __name__ == '__main__':
    main()
