-- +goose Up
CREATE TABLE products (
    id          uuid PRIMARY KEY,
    sku         text        NOT NULL UNIQUE,
    name        text        NOT NULL,
    description text        NOT NULL DEFAULT '',
    price_cents bigint      NOT NULL CHECK (price_cents >= 0),
    -- Las restricciones CHECK son la última barrera contra la sobreventa.
    available   integer     NOT NULL DEFAULT 0 CHECK (available >= 0),
    reserved    integer     NOT NULL DEFAULT 0 CHECK (reserved >= 0),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE reservations (
    id              uuid PRIMARY KEY,
    idempotency_key text        NOT NULL UNIQUE,
    status          text        NOT NULL CHECK (status IN ('ACTIVE', 'RELEASED')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    released_at     timestamptz
);

CREATE TABLE reservation_items (
    reservation_id uuid    NOT NULL REFERENCES reservations (id) ON DELETE CASCADE,
    product_id     uuid    NOT NULL REFERENCES products (id),
    quantity       integer NOT NULL CHECK (quantity > 0),
    PRIMARY KEY (reservation_id, product_id)
);

-- +goose Down
DROP TABLE reservation_items;
DROP TABLE reservations;
DROP TABLE products;
