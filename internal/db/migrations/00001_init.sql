-- +goose Up

-- One row per end customer of the online shop.
-- The id comes from the shop itself (its own user id), so it is TEXT, not a number we generate.
CREATE TABLE users (
    id            TEXT PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 128),
    attributes    JSONB NOT NULL DEFAULT '{}'::jsonb,   -- email, city, plan, ...
    first_seen_at TIMESTAMPTZ NOT NULL,
    last_seen_at  TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Everything a user does on the site: page_view, product_view, add_to_cart, purchase, ...
CREATE TABLE events (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- Sent by the client. If the same event arrives twice (a retry after a timeout),
    -- the UNIQUE constraint lets us keep only the first copy.
    event_id    UUID NOT NULL UNIQUE,
    user_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name        TEXT NOT NULL CHECK (name ~ '^[a-z][a-z0-9_]{0,63}$'),
    properties  JSONB NOT NULL DEFAULT '{}'::jsonb,     -- product_id, price, category, ...
    occurred_at TIMESTAMPTZ NOT NULL,                   -- when it happened on the site
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()      -- when the API stored it
);
-- No extra indexes yet on purpose: the performance step measures the segment queries first,
-- then adds the index they need and compares EXPLAIN ANALYZE before and after.

-- A saved audience definition, e.g. "added to cart in the last 7 days but did not buy".
CREATE TABLE segments (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE CHECK (length(name) BETWEEN 1 AND 100),
    description TEXT NOT NULL DEFAULT '',
    rules       JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE segments;
DROP TABLE events;
DROP TABLE users;
