package store

// migrations are the SPEC section 10 schema, applied in order (migration 2 makes the IDs non-reusing). PRAGMA user_version counts the ones that ran.
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
`,
	// Migration 2: file, item, folder and generation IDs come from AUTOINCREMENT sequences, so a deleted row's
	// ID is never handed out again. A deleted highest row would otherwise let a new file or item take its ID,
	// and an outstanding job or a cached seed would then name the wrong object. Existing IDs are kept.
	`
CREATE TABLE folders_v2 (id INTEGER PRIMARY KEY AUTOINCREMENT, path TEXT NOT NULL UNIQUE,
                         ignore TEXT NOT NULL DEFAULT '', added_at INTEGER NOT NULL);
INSERT INTO folders_v2 (id, path, ignore, added_at) SELECT id, path, ignore, added_at FROM folders;
DROP TABLE folders;
ALTER TABLE folders_v2 RENAME TO folders;

CREATE TABLE generations_v2 (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  text_quant TEXT NOT NULL, enc_quant TEXT NOT NULL, model_rev TEXT NOT NULL,
  state TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
INSERT INTO generations_v2 (id, text_quant, enc_quant, model_rev, state, created_at)
  SELECT id, text_quant, enc_quant, model_rev, state, created_at FROM generations;
DROP TABLE generations;
ALTER TABLE generations_v2 RENAME TO generations;

CREATE TABLE files_v2 (
  id INTEGER PRIMARY KEY AUTOINCREMENT, folder_id INTEGER NOT NULL, path TEXT NOT NULL UNIQUE,
  kind TEXT NOT NULL,
  size INTEGER NOT NULL, mtime INTEGER NOT NULL
);
INSERT INTO files_v2 (id, folder_id, path, kind, size, mtime) SELECT id, folder_id, path, kind, size, mtime FROM files;
DROP TABLE files;
ALTER TABLE files_v2 RENAME TO files;
CREATE INDEX files_folder ON files (folder_id);

CREATE TABLE items_v2 (
  id INTEGER PRIMARY KEY AUTOINCREMENT, gen INTEGER NOT NULL, file_id INTEGER NOT NULL,
  unit INTEGER NOT NULL DEFAULT 0,
  start INTEGER, "end" INTEGER,
  modality TEXT NOT NULL,
  snippet TEXT, embedding BLOB NOT NULL
);
INSERT INTO items_v2 (id, gen, file_id, unit, start, "end", modality, snippet, embedding)
  SELECT id, gen, file_id, unit, start, "end", modality, snippet, embedding FROM items;
DROP TABLE items;
ALTER TABLE items_v2 RENAME TO items;
CREATE INDEX items_gen_file ON items (gen, file_id);
`}
