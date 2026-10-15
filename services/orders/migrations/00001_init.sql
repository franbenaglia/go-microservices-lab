-- +goose Up
CREATE TABLE orders (
    id               uuid PRIMARY KEY,
    status           text        NOT NULL CHECK (status IN ('PENDING', 'CONFIRMED', 'REJECTED', 'CANCELLED')),
    total_cents      bigint      NOT NULL DEFAULT 0 CHECK (total_cents >= 0),
    reservation_id   uuid,
    rejection_code   text        NOT NULL DEFAULT '',
    rejection_reason text        NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX orders_status_created_idx ON orders (status, created_at DESC);
CREATE INDEX orders_created_idx ON orders (created_at DESC);

-- product_id es una referencia lógica a Inventory: no hay clave foránea porque
-- cada servicio tiene su propia base de datos.
CREATE TABLE order_items (
    order_id         uuid    NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    product_id       uuid    NOT NULL,
    sku              text    NOT NULL DEFAULT '',
    name             text    NOT NULL DEFAULT '',
    quantity         integer NOT NULL CHECK (quantity > 0),
    unit_price_cents bigint  NOT NULL DEFAULT 0 CHECK (unit_price_cents >= 0),
    PRIMARY KEY (order_id, product_id)
);

-- +goose Down
DROP TABLE order_items;
DROP TABLE orders;
