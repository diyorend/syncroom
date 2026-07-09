CREATE TABLE rooms (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code        TEXT UNIQUE NOT NULL,        -- short shareable code, e.g. "BLUE-FOX-42"
    creator_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    video_url   TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_rooms_code ON rooms(code);
