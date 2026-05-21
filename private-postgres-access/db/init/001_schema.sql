CREATE TABLE IF NOT EXISTS notes (
  id BIGSERIAL PRIMARY KEY,
  body TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO notes (body)
VALUES ('hello from a private PostgreSQL service')
ON CONFLICT DO NOTHING;
