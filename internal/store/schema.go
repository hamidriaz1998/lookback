package store

// migrations are the SPEC section 10 schema, applied in order. PRAGMA user_version counts the ones that ran.
// Times are Unix nanoseconds (files.mtime, file_index.mtime, folders.added_at, generations.created_at,
// combos.installed_at). The schema has no foreign keys: the write methods delete dependent rows themselves,
// in the same transaction.
var migrations = []string{`
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);

CREATE TABLE combos (
  id TEXT PRIMARY KEY,
  backend TEXT NOT NULL, text_quant TEXT NOT NULL, enc_quant TEXT NOT NULL,
  installed_at INTEGER NOT NULL, bench_json TEXT
);

CREATE TABLE generations (
  id INTEGER PRIMARY KEY,
  text_quant TEXT NOT NULL, enc_quant TEXT NOT NULL, model_rev TEXT NOT NULL,
  state TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE folders (id INTEGER PRIMARY KEY, path TEXT NOT NULL UNIQUE,
                      ignore TEXT NOT NULL DEFAULT '', added_at INTEGER NOT NULL);

CREATE TABLE files (
  id INTEGER PRIMARY KEY, folder_id INTEGER NOT NULL, path TEXT NOT NULL UNIQUE,
  kind TEXT NOT NULL,
  size INTEGER NOT NULL, mtime INTEGER NOT NULL
);
CREATE INDEX files_folder ON files (folder_id);

CREATE TABLE file_index (
  file_id INTEGER NOT NULL, gen INTEGER NOT NULL,
  size INTEGER NOT NULL, mtime INTEGER NOT NULL,
  status TEXT NOT NULL,
  error TEXT, tokens INTEGER, ms INTEGER,
  PRIMARY KEY (file_id, gen)
);
CREATE INDEX file_index_gen ON file_index (gen);

CREATE TABLE items (
  id INTEGER PRIMARY KEY, gen INTEGER NOT NULL, file_id INTEGER NOT NULL,
  unit INTEGER NOT NULL DEFAULT 0,
  start INTEGER, "end" INTEGER,
  modality TEXT NOT NULL,
  snippet TEXT, embedding BLOB NOT NULL
);
CREATE INDEX items_gen_file ON items (gen, file_id);
`}
