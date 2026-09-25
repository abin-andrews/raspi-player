// Package search provides a SQLite FTS5-backed full-text search index over
// the URL collection.
package search

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver (pure Go, no CGO)
)

// defaultLimit caps result size when Search/List is called with a
// non-positive limit, so a careless caller can't accidentally pull back the
// entire index.
const defaultLimit = 100

// Index wraps a *sql.DB backed by a SQLite FTS5 virtual table for indexing
// and searching URLs by url, title, artist, album, and tags.
type Index struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at dsn (which may be
// ":memory:" for an in-memory database, or a file path) and ensures the FTS5
// virtual table used to store the index exists, with artist/album columns.
func Open(dsn string) (*Index, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("search: open %q: %w", dsn, err)
	}

	if err := migrateSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Index{db: db}, nil
}

// migrateSchema ensures the urls FTS5 table exists with the current
// (url, title, artist, album, tags) column set, migrating a database file
// from before artist/album were their own columns (they used to be folded
// into tags, with no way to group by them). FTS5 virtual tables don't
// support ALTER TABLE ADD COLUMN, so migration works by renaming the old
// table aside, creating the new one, and copying the old rows across with
// empty artist/album (which get filled in the next time each url is
// re-indexed).
func migrateSchema(db *sql.DB) error {
	hasTable, err := tableExists(db, "urls")
	if err != nil {
		return fmt.Errorf("search: check for existing table: %w", err)
	}
	if !hasTable {
		if _, err := db.Exec(`CREATE VIRTUAL TABLE urls USING fts5(url, title, artist, album, tags);`); err != nil {
			return fmt.Errorf("search: create fts5 table: %w", err)
		}
		return nil
	}

	hasArtist, err := columnExists(db, "urls", "artist")
	if err != nil {
		return fmt.Errorf("search: check for artist column: %w", err)
	}
	if hasArtist {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("search: begin migration tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`ALTER TABLE urls RENAME TO urls_pre_artist_album`); err != nil {
		return fmt.Errorf("search: rename old table: %w", err)
	}
	if _, err := tx.Exec(`CREATE VIRTUAL TABLE urls USING fts5(url, title, artist, album, tags);`); err != nil {
		return fmt.Errorf("search: create migrated fts5 table: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO urls (url, title, artist, album, tags) SELECT url, title, '', '', tags FROM urls_pre_artist_album`,
	); err != nil {
		return fmt.Errorf("search: copy rows into migrated table: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE urls_pre_artist_album`); err != nil {
		return fmt.Errorf("search: drop old table: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("search: commit migration: %w", err)
	}
	return nil
}

func tableExists(db *sql.DB, name string) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func columnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			colType    string
			notNull    int
			defaultVal any
			pk         int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultVal, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// Close closes the underlying database.
func (idx *Index) Close() error {
	return idx.db.Close()
}

// IndexURL adds url (with its title, artist, album, and tags) to the
// index. If url is already present, its existing row is replaced
// (delete-then-insert keyed on the url column), so re-indexing on update is
// idempotent and never produces duplicate results — this is also how a
// thin entry (e.g. just a URL, indexed when it was first queued) gets
// enriched later once real tags are known (e.g. once mpd actually starts
// playing it and reports Title/Artist/Album).
func (idx *Index) IndexURL(url, title, artist, album, tags string) error {
	tx, err := idx.db.Begin()
	if err != nil {
		return fmt.Errorf("search: begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM urls WHERE url = ?`, url); err != nil {
		return fmt.Errorf("search: delete existing %q: %w", url, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO urls (url, title, artist, album, tags) VALUES (?, ?, ?, ?, ?)`,
		url, title, artist, album, tags,
	); err != nil {
		return fmt.Errorf("search: insert %q: %w", url, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("search: commit: %w", err)
	}
	return nil
}

// Get looks up the exact url (no FTS matching involved), returning
// ok=false rather than an error if it isn't indexed. Used to check whether
// a URL is already known before doing avoidable duplicate work (e.g.
// re-fetching metadata for a track that's already indexed with real
// artist/album tags).
func (idx *Index) Get(url string) (Result, bool, error) {
	row := idx.db.QueryRow(
		`SELECT url, title, artist, album, tags FROM urls WHERE url = ? LIMIT 1`,
		url,
	)
	var r Result
	if err := row.Scan(&r.URL, &r.Title, &r.Artist, &r.Album, &r.Tags); err != nil {
		if err == sql.ErrNoRows {
			return Result{}, false, nil
		}
		return Result{}, false, fmt.Errorf("search: get %q: %w", url, err)
	}
	return r, true, nil
}

// Result is a single indexed entry.
type Result struct {
	URL    string `json:"url"`
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Album  string `json:"album"`
	Tags   string `json:"tags"`
}

// Search runs an FTS5 MATCH query against the index and returns matches
// ordered by relevance (best match first). If limit is <= 0, a generous
// default (100) is applied instead of leaving the result set unbounded.
func (idx *Index) Search(query string, limit int) ([]Result, error) {
	if limit <= 0 {
		limit = defaultLimit
	}

	rows, err := idx.db.Query(
		`SELECT url, title, artist, album, tags FROM urls WHERE urls MATCH ? ORDER BY rank LIMIT ?`,
		query, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("search: query %q: %w", query, err)
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.URL, &r.Title, &r.Artist, &r.Album, &r.Tags); err != nil {
			return nil, fmt.Errorf("search: scan: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search: rows: %w", err)
	}

	return results, nil
}

// List returns every indexed entry, most-recently-indexed first, for
// browsing the full collection without a search query (a "media library"
// view) — distinct from Search, which requires an FTS5 MATCH query and
// errors on an empty one. limit<=0 uses the same defaultLimit as Search;
// offset<0 is treated as 0.
func (idx *Index) List(limit, offset int) ([]Result, error) {
	if limit <= 0 {
		limit = defaultLimit
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := idx.db.Query(
		`SELECT url, title, artist, album, tags FROM urls ORDER BY rowid DESC LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("search: list: %w", err)
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.URL, &r.Title, &r.Artist, &r.Album, &r.Tags); err != nil {
			return nil, fmt.Errorf("search: scan: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search: rows: %w", err)
	}
	return results, nil
}
