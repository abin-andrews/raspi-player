// Package search provides a SQLite FTS5-backed full-text search index over
// the URL collection.
package search

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver (pure Go, no CGO)
)

// defaultLimit caps result size when Search is called with a non-positive
// limit, so a careless caller can't accidentally pull back the entire index.
const defaultLimit = 100

// Index wraps a *sql.DB backed by a SQLite FTS5 virtual table for indexing
// and searching URLs by url, title, and tags.
type Index struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at dsn (which may be
// ":memory:" for an in-memory database, or a file path) and ensures the FTS5
// virtual table used to store the index exists.
func Open(dsn string) (*Index, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("search: open %q: %w", dsn, err)
	}

	if _, err := db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS urls USING fts5(url, title, tags);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("search: create fts5 table: %w", err)
	}

	return &Index{db: db}, nil
}

// Close closes the underlying database.
func (idx *Index) Close() error {
	return idx.db.Close()
}

// IndexURL adds url (with its title and tags) to the index. If url is
// already present, its existing row is replaced (delete-then-insert keyed on
// the url column), so re-indexing on update is idempotent and never produces
// duplicate results.
func (idx *Index) IndexURL(url, title, tags string) error {
	tx, err := idx.db.Begin()
	if err != nil {
		return fmt.Errorf("search: begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM urls WHERE url = ?`, url); err != nil {
		return fmt.Errorf("search: delete existing %q: %w", url, err)
	}
	if _, err := tx.Exec(`INSERT INTO urls (url, title, tags) VALUES (?, ?, ?)`, url, title, tags); err != nil {
		return fmt.Errorf("search: insert %q: %w", url, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("search: commit: %w", err)
	}
	return nil
}

// Result is a single search match.
type Result struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Tags  string `json:"tags"`
}

// Search runs an FTS5 MATCH query against the index and returns matches
// ordered by relevance (best match first). If limit is <= 0, a generous
// default (100) is applied instead of leaving the result set unbounded.
func (idx *Index) Search(query string, limit int) ([]Result, error) {
	if limit <= 0 {
		limit = defaultLimit
	}

	rows, err := idx.db.Query(
		`SELECT url, title, tags FROM urls WHERE urls MATCH ? ORDER BY rank LIMIT ?`,
		query, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("search: query %q: %w", query, err)
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.URL, &r.Title, &r.Tags); err != nil {
			return nil, fmt.Errorf("search: scan: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search: rows: %w", err)
	}

	return results, nil
}
