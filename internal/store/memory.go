package store

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Memory: durable notes about a project — decisions, conventions, gotchas,
// how-tos — or about the user (no project: preferences that hold
// everywhere). Claude sessions start with the relevant ones (ContextForPath),
// recall more on demand and save what they learn through MCP; the Assistant
// and Telegram read and write the same list. memories_fts indexes content
// for search; every write here keeps it in step.

var (
	MemoryKinds   = []string{"fact", "decision", "convention", "gotcha", "preference", "howto"}
	MemorySources = []string{"user", "claude", "assistant", "telegram"}
)

const (
	memoryMinLen = 3
	memoryMaxLen = 2000
)

type Memory struct {
	ID             int64      `json:"id"`
	ProjectKey     *string    `json:"project_key"` // nil: about the user, every project
	RepoID         *int64     `json:"repo_id"`
	RepoName       *string    `json:"repo_name"`
	Kind           string     `json:"kind"`
	Content        string     `json:"content"`
	Source         string     `json:"source"`
	Origin         string     `json:"origin"`
	Pinned         bool       `json:"pinned"`
	Recalls        int        `json:"recalls"`
	LastRecalledAt *time.Time `json:"last_recalled_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Line is the memory as a session reads it: "[m12] (gotcha) …".
func (m Memory) Line(maxChars int) string {
	c := strings.Join(strings.Fields(m.Content), " ")
	if maxChars > 0 && len(c) > maxChars {
		c = c[:maxChars] + "…"
	}
	tag := m.Kind
	if m.RepoName != nil {
		tag += ", " + *m.RepoName
	}
	if m.Pinned {
		tag += ", pinned"
	}
	return fmt.Sprintf("[m%d] (%s) %s", m.ID, tag, c)
}

type MemoryInput struct {
	ProjectKey string `json:"project_key"` // "" = about the user
	Repo       string `json:"repo"`        // repo name within the project (optional)
	Kind       string `json:"kind"`
	Content    string `json:"content"`
	Pinned     bool   `json:"pinned"`
	Source     string `json:"-"`
	Origin     string `json:"-"`
}

// MemorySaved is what a write returns: the memory, whether it already
// existed word for word, and similar ones the writer may want to merge.
type MemorySaved struct {
	Memory    *Memory  `json:"memory"`
	Duplicate bool     `json:"duplicate"`
	Similar   []Memory `json:"similar"`
}

type MemoryFilter struct {
	ProjectKey string // one project ("" = every memory)
	Global     bool   // only the ones about no project
	WithGlobal bool   // with ProjectKey: the global ones too
	Query      string // full-text search, best match first
	Limit      int
}

const memorySelect = `SELECT m.id, p.key, m.repo_id, r.name, m.kind, m.content, m.source, m.origin, m.pinned,
       m.recalls, m.last_recalled_at, m.created_at, m.updated_at
  FROM memories m
  LEFT JOIN projects p ON p.id = m.project_id
  LEFT JOIN repos r ON r.id = m.repo_id`

func scanMemory(row interface{ Scan(...any) error }) (*Memory, error) {
	var m Memory
	if err := row.Scan(&m.ID, &m.ProjectKey, &m.RepoID, &m.RepoName, &m.Kind, &m.Content, &m.Source, &m.Origin,
		&m.Pinned, &m.Recalls, &m.LastRecalledAt, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, mapErr(err)
	}
	return &m, nil
}

func (s *Store) queryMemories(ctx context.Context, q string, args ...any) ([]Memory, error) {
	rows, err := s.DB.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Memory{}
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func (s *Store) MemoryByID(ctx context.Context, id int64) (*Memory, error) {
	return scanMemory(s.DB.QueryRow(ctx, memorySelect+` WHERE m.id = $1`, id))
}

// ListMemories lists or searches. Without a query the pinned ones come
// first, then the most recently changed.
func (s *Store) ListMemories(ctx context.Context, f MemoryFilter) ([]Memory, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	var where []string
	args := []any{}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	switch {
	case f.Global:
		where = append(where, "m.project_id IS NULL")
	case f.ProjectKey != "":
		cond := "p.key = " + arg(strings.ToUpper(f.ProjectKey))
		if f.WithGlobal {
			cond = "(" + cond + " OR m.project_id IS NULL)"
		}
		where = append(where, cond)
	}
	if q := ftsQuery(f.Query); q != "" {
		where = append(where, "memories_fts MATCH "+arg(q))
		sql := strings.Replace(memorySelect, "FROM memories m", "FROM memories_fts JOIN memories m ON m.id = memories_fts.rowid", 1) +
			" WHERE " + strings.Join(where, " AND ") + " ORDER BY bm25(memories_fts) - m.pinned LIMIT " + arg(f.Limit)
		return s.queryMemories(ctx, sql, args...)
	} else if strings.TrimSpace(f.Query) != "" {
		return []Memory{}, nil // nothing searchable in it (only stopwords or punctuation)
	}
	sql := memorySelect
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	sql += " ORDER BY m.pinned DESC, m.updated_at DESC, m.id DESC LIMIT " + arg(f.Limit)
	return s.queryMemories(ctx, sql, args...)
}

// CreateMemory saves a memory unless the same words are already there (then
// that one is returned, marked Duplicate). Similar memories come back too, so
// the writer can merge instead of piling up near-copies.
func (s *Store) CreateMemory(ctx context.Context, in MemoryInput) (*MemorySaved, error) {
	in.Content = strings.TrimSpace(in.Content)
	if n := len([]rune(in.Content)); n < memoryMinLen || n > memoryMaxLen {
		return nil, invalid("content", "write %d–%d characters", memoryMinLen, memoryMaxLen)
	}
	if in.Kind == "" {
		in.Kind = "fact"
	}
	if !OneOf(in.Kind, MemoryKinds) {
		return nil, invalid("kind", "must be one of %s", strings.Join(MemoryKinds, ", "))
	}
	if in.Source == "" {
		in.Source = "user"
	}
	if !OneOf(in.Source, MemorySources) {
		return nil, invalid("source", "unknown source")
	}
	projectID, repoID, err := s.memoryScope(ctx, in.ProjectKey, in.Repo)
	if err != nil {
		return nil, err
	}

	// The same words in the same place: keep the one there is.
	var dupID int64
	err = s.DB.QueryRow(ctx, `SELECT id FROM memories WHERE project_id IS $1 AND lower(trim(content)) = lower($2) LIMIT 1`,
		projectID, in.Content).Scan(&dupID)
	if err == nil {
		m, err := s.MemoryByID(ctx, dupID)
		if err != nil {
			return nil, err
		}
		return &MemorySaved{Memory: m, Duplicate: true, Similar: []Memory{}}, nil
	}

	var id int64
	err = s.tx(ctx, func(tx pgxTx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO memories (project_id, repo_id, kind, content, source, origin, pinned)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
			projectID, repoID, in.Kind, in.Content, in.Source, truncate(in.Origin, 200), in.Pinned).Scan(&id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO memories_fts (rowid, content) VALUES ($1, $2)`, id, in.Content)
		return err
	})
	if err != nil {
		return nil, err
	}
	m, err := s.MemoryByID(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &MemorySaved{Memory: m, Similar: []Memory{}}
	f := MemoryFilter{Query: in.Content, Limit: 4, Global: in.ProjectKey == ""}
	if in.ProjectKey != "" {
		f.ProjectKey = in.ProjectKey
	}
	if similar, err := s.ListMemories(ctx, f); err == nil {
		for _, x := range similar {
			if x.ID != id && len(out.Similar) < 3 {
				out.Similar = append(out.Similar, x)
			}
		}
	}
	return out, nil
}

// memoryScope resolves a project key and a repo name within it.
func (s *Store) memoryScope(ctx context.Context, projectKey, repo string) (*int64, *int64, error) {
	projectKey, repo = strings.ToUpper(strings.TrimSpace(projectKey)), strings.TrimSpace(repo)
	if projectKey == "" {
		if repo != "" {
			return nil, nil, invalid("repo", "a repo needs its project")
		}
		return nil, nil, nil
	}
	pid, err := s.ProjectIDByKey(ctx, projectKey)
	if err != nil {
		return nil, nil, invalid("project_key", "no project %s", projectKey)
	}
	if repo == "" {
		return &pid, nil, nil
	}
	var rid int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM repos WHERE project_id = $1 AND lower(name) = lower($2)`, pid, repo).Scan(&rid); err != nil {
		return nil, nil, invalid("repo", "%s has no repo %q", projectKey, repo)
	}
	return &pid, &rid, nil
}

// MemoryPatch changes a memory; nil fields stay as they are.
type MemoryPatch struct {
	Content    *string `json:"content"`
	Kind       *string `json:"kind"`
	Pinned     *bool   `json:"pinned"`
	ProjectKey *string `json:"project_key"` // "" moves it to the global memory
}

func (s *Store) UpdateMemory(ctx context.Context, id int64, p MemoryPatch) (*Memory, error) {
	m, err := s.MemoryByID(ctx, id)
	if err != nil {
		return nil, err
	}
	content, kind, pinned := m.Content, m.Kind, m.Pinned
	if p.Content != nil {
		content = strings.TrimSpace(*p.Content)
		if n := len([]rune(content)); n < memoryMinLen || n > memoryMaxLen {
			return nil, invalid("content", "write %d–%d characters", memoryMinLen, memoryMaxLen)
		}
	}
	if p.Kind != nil {
		if !OneOf(*p.Kind, MemoryKinds) {
			return nil, invalid("kind", "must be one of %s", strings.Join(MemoryKinds, ", "))
		}
		kind = *p.Kind
	}
	if p.Pinned != nil {
		pinned = *p.Pinned
	}
	err = s.tx(ctx, func(tx pgxTx) error {
		if p.ProjectKey != nil {
			pid, _, err := s.memoryScope(ctx, *p.ProjectKey, "")
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE memories SET project_id = $2, repo_id = NULL WHERE id = $1`, id, pid); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE memories SET content = $2, kind = $3, pinned = $4, updated_at = now() WHERE id = $1`,
			id, content, kind, pinned); err != nil {
			return err
		}
		if content == m.Content {
			return nil
		}
		if _, err := tx.Exec(ctx, `DELETE FROM memories_fts WHERE rowid = $1`, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO memories_fts (rowid, content) VALUES ($1, $2)`, id, content)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.MemoryByID(ctx, id)
}

func (s *Store) DeleteMemory(ctx context.Context, id int64) error {
	return s.tx(ctx, func(tx pgxTx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM memories WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `DELETE FROM memories_fts WHERE rowid = $1`, id)
		return err
	})
}

// MarkRecalled counts that these memories were handed to a session on
// demand (a search or a match, not the list every session starts with).
func (s *Store) MarkRecalled(ctx context.Context, ids []int64) {
	for _, id := range ids {
		_, _ = s.DB.Exec(ctx, `UPDATE memories SET recalls = recalls + 1, last_recalled_at = now() WHERE id = $1`, id)
	}
}

// pruneMemoryIndex drops index rows whose memory is gone (a deleted project
// takes its memories with it by cascade, which the index cannot follow).
func (s *Store) pruneMemoryIndex(ctx context.Context) {
	_, _ = s.DB.Exec(ctx, `DELETE FROM memories_fts WHERE rowid NOT IN (SELECT id FROM memories)`)
}

// contextMemories are what a session in the project starts with: pinned
// first, then the most used and the newest — the project-wide ones plus
// those of the repo it is in — and a few about the user.
func (s *Store) contextMemories(ctx context.Context, projectKey string, repoID *int64) (project, global []Memory, err error) {
	project, err = s.queryMemories(ctx, memorySelect+` WHERE p.key = $1 AND (m.repo_id IS NULL OR m.repo_id IS $2)
		ORDER BY m.pinned DESC, m.recalls DESC, m.updated_at DESC LIMIT 40`, projectKey, repoID)
	if err != nil {
		return nil, nil, err
	}
	global, err = s.queryMemories(ctx, memorySelect+` WHERE m.project_id IS NULL
		ORDER BY m.pinned DESC, m.recalls DESC, m.updated_at DESC LIMIT 10`)
	return project, global, err
}

// CountMemories per project key ("" = global).
func (s *Store) CountMemories(ctx context.Context) (map[string]int, error) {
	rows, err := s.DB.Query(ctx, `SELECT coalesce(p.key, ''), count(*) FROM memories m
		LEFT JOIN projects p ON p.id = m.project_id GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

var memoryStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "but": true, "not": true, "you": true, "all": true,
	"can": true, "has": true, "was": true, "one": true, "our": true, "out": true, "use": true, "how": true,
	"this": true, "that": true, "with": true, "from": true, "into": true, "have": true, "what": true, "when": true,
	"where": true, "which": true, "will": true, "also": true, "them": true, "then": true, "than": true, "some": true,
	"more": true, "only": true, "just": true, "like": true, "want": true, "need": true, "does": true, "they": true,
	"there": true, "their": true, "about": true, "should": true, "would": true, "could": true, "please": true,
	"its": true, "it's": true, "is": true, "it": true, "to": true, "of": true, "in": true, "on": true, "an": true,
	"be": true, "do": true, "we": true, "my": true, "me": true, "or": true, "as": true, "at": true, "by": true,
	"if": true, "so": true, "no": true, "up": true, "a": true, "i": true,
}

// ftsQuery turns free text into an FTS5 query: its significant words, each
// as a quoted prefix, any of them matching (bm25 ranks those with more
// first). "" when nothing is left.
func ftsQuery(text string) string {
	seen := map[string]bool{}
	var terms []string
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		if len([]rune(w)) < 2 || memoryStopwords[w] || seen[w] {
			continue
		}
		seen[w] = true
		terms = append(terms, `"`+w+`"*`)
		if len(terms) == 24 {
			break
		}
	}
	return strings.Join(terms, " OR ")
}
