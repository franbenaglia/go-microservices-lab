#!/bin/sh
# Crea una base de datos y un usuario por microservicio. Ningún servicio tiene
# permisos sobre la base del otro. Solo se ejecuta al inicializar el volumen.
# También crea "labtest" para las pruebas de integración (make test-integration).
set -eu

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
  -v inv_pwd="$INVENTORY_DB_PASSWORD" -v ord_pwd="$ORDERS_DB_PASSWORD" <<'SQL'
CREATE ROLE inventory LOGIN PASSWORD :'inv_pwd';
CREATE ROLE orders LOGIN PASSWORD :'ord_pwd';
CREATE DATABASE inventory OWNER inventory;
CREATE DATABASE orders OWNER orders;
CREATE DATABASE labtest;
REVOKE ALL ON DATABASE inventory FROM PUBLIC;
REVOKE ALL ON DATABASE orders FROM PUBLIC;
SQL
