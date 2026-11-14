package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	Pool       *pgxpool.Pool
	PublicOnly bool
}

func Open(ctx context.Context, url string) (*DB, error) {
	if url == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err = p.Ping(ctx); err != nil {
		p.Close()
		return nil, err
	}
	return &DB{Pool: p}, nil
}

// Migrate applies pending migrations without changing any role's privileges.
func (d *DB) Migrate(ctx context.Context, dir string) error {
	return d.MigrateGranting(ctx, dir, "")
}

// MigrateGranting applies pending migrations and then, when reader is not
// empty, grants that existing role USAGE on the migration schema (public in
// deployments) and SELECT on every table and view in it, including tables the
// run created. An empty reader behaves exactly like Migrate.
func (d *DB) MigrateGranting(ctx context.Context, dir, reader string) error {
	if reader != "" && (len(reader) > 63 || strings.ContainsRune(reader, 0) || strings.TrimSpace(reader) != reader) {
		return fmt.Errorf("reader role name is invalid")
	}
	c, err := d.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer c.Release()
	if reader != "" {
		var exists bool
		if err = c.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, reader).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("reader role %q does not exist", reader)
		}
	}
	if _, err = c.Exec(ctx, `SELECT pg_advisory_lock(79202173)`); err != nil {
		return err
	}
	defer c.Exec(context.Background(), `SELECT pg_advisory_unlock(79202173)`)
	if _, err = c.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_versions (name text PRIMARY KEY,sha256 text NOT NULL)`); err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	if len(files) == 0 {
		return fmt.Errorf("no migrations")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		name := filepath.Base(f)
		hash := domain.Digest(b)
		var old string
		err = c.QueryRow(ctx, `SELECT sha256 FROM schema_versions WHERE name=$1`, name).Scan(&old)
		if err == nil {
			if old != hash {
				return fmt.Errorf("applied migration checksum changed: %s", name)
			}
			continue
		}
		if err != pgx.ErrNoRows {
			return err
		}
		tx, err := c.Begin(ctx)
		if err != nil {
			return err
		}
		sql := string(b)
		if _, err = tx.Exec(ctx, sql); err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO schema_versions VALUES($1,$2)`, name, hash)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
	}
	if reader == "" {
		return nil
	}
	var schema string
	if err = c.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return err
	}
	schemaSQL, readerSQL := pgx.Identifier{schema}.Sanitize(), pgx.Identifier{reader}.Sanitize()
	_, err = c.Exec(ctx, "GRANT USAGE ON SCHEMA "+schemaSQL+" TO "+readerSQL+"; GRANT SELECT ON ALL TABLES IN SCHEMA "+schemaSQL+" TO "+readerSQL)
	return err
}
func (d *DB) Collection(ctx context.Context, c domain.Collection) error {
	if err := c.Validate(); err != nil {
		return err
	}
	// Existing policy is immutable; changing storage ownership requires a new reviewed migration.
	var existing domain.Collection
	err := d.Pool.QueryRow(ctx, `SELECT id,policy,budget,reserve,source_item FROM collections WHERE id=$1`, c.ID).Scan(&existing.ID, &existing.Policy, &existing.Budget, &existing.Reserve, &existing.SourceItem)
	if err == nil && existing != c {
		return fmt.Errorf("existing collection policy or budget cannot be changed implicitly")
	}
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	_, err = d.Pool.Exec(ctx, `INSERT INTO collections(id,policy,budget,reserve,source_item) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO NOTHING`, c.ID, c.Policy, c.Budget, c.Reserve, c.SourceItem)
	if err != nil {
		return err
	}
	_, err = d.Pool.Exec(ctx, `INSERT INTO worker_state(collection_id) VALUES($1) ON CONFLICT DO NOTHING`, c.ID)
	return err
}
func (d *DB) GetCollection(ctx context.Context, id string) (domain.Collection, error) {
	var c domain.Collection
	err := d.Pool.QueryRow(ctx, `SELECT id,policy,budget,reserve,source_item FROM collections WHERE id=$1`, id).Scan(&c.ID, &c.Policy, &c.Budget, &c.Reserve, &c.SourceItem)
	return c, err
}

// ReviewBudget explicitly increases reviewed local limits without changing
// collection identity, delivery policy, or source ownership. Expected limits
// prevent applying a stale approval over another operator's review.
func (d *DB) ReviewBudget(ctx context.Context, id string, expectedBudget, expectedReserve, budget, reserve int64, notes string) error {
	if strings.TrimSpace(notes) == "" || len(notes) > 4000 {
		return fmt.Errorf("budget review requires bounded authorization notes")
	}
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var c domain.Collection
	err = tx.QueryRow(ctx, `SELECT id,policy,budget,reserve,source_item FROM collections WHERE id=$1 FOR UPDATE`, id).Scan(&c.ID, &c.Policy, &c.Budget, &c.Reserve, &c.SourceItem)
	if err != nil {
		return err
	}
	if c.Policy != domain.Local || c.ID == domain.DemocracyNow || c.Budget != expectedBudget || c.Reserve != expectedReserve || budget < c.Budget || reserve < c.Reserve || (budget == c.Budget && reserve == c.Reserve) {
		return fmt.Errorf("budget review requires matching prior local limits and an explicit increase")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO collection_budget_reviews(collection_id,old_budget,old_reserve,new_budget,new_reserve,notes) VALUES($1,$2,$3,$4,$5,$6)`, id, c.Budget, c.Reserve, budget, reserve, notes); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE collections SET budget=$2,reserve=$3 WHERE id=$1`, id, budget, reserve); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (d *DB) Snapshot(ctx context.Context, item string, raw []byte, status int, etag, modified string, e *domain.Episode) error {
	if !domain.ValidID(item) || !json.Valid(raw) {
		return fmt.Errorf("invalid source snapshot")
	}
	if e != nil {
		if e.ID != item {
			return fmt.Errorf("snapshot episode identity mismatch")
		}
		if err := e.Validate(); err != nil {
			return err
		}
	}
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	hash := domain.Digest(raw)
	if _, err = tx.Exec(ctx, `INSERT INTO snapshots(sha256,item_id,raw,parsed) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, hash, item, raw, json.RawMessage(raw)); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO observations(item_id,snapshot_hash,status,etag,modified) VALUES($1,$2,$3,$4,$5)`, item, hash, status, etag, modified); err != nil {
		return err
	}
	if e != nil {
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		var prior string
		err = tx.QueryRow(ctx, `SELECT collection_id FROM episodes WHERE id=$1`, item).Scan(&prior)
		if err == nil && prior != e.CollectionID {
			return fmt.Errorf("episode collection identity changed")
		}
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO episodes(id,collection_id,source_hash,source) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET source_hash=excluded.source_hash,source=excluded.source,unavailable=false,updated_at=now()`, item, e.CollectionID, hash, b)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (d *DB) entries(ctx context.Context, ids []string, summaries bool, collections []string) (map[string]domain.Entry, error) {
	source := "e.source"
	if summaries {
		source = `(e.source-'files') || jsonb_build_object('files',COALESCE((SELECT jsonb_agg(f) FROM jsonb_array_elements(CASE WHEN jsonb_typeof(e.source->'files')='array' THEN e.source->'files' ELSE '[]'::jsonb END) f WHERE f->>'name'=e.decision->>'file' OR f->>'name'=e.decision->'artwork'->>'file' OR f->>'name' IN (SELECT caption->>'file' FROM jsonb_array_elements(CASE WHEN jsonb_typeof(e.decision->'processed_video'->'captions')='array' THEN e.decision->'processed_video'->'captions' ELSE '[]'::jsonb END) caption)),'[]'::jsonb))`
	}
	rows, err := d.Pool.Query(ctx, `SELECT `+source+`,e.decision,c.policy,e.source_hash,e.unavailable,e.local_path,e.local_sha256,e.local_file,e.local_snapshot,c.budget,COALESCE(s.parsed->'metadata'->'subject','null'::jsonb),COALESCE(s.parsed->'metadata'->'language','null'::jsonb) FROM episodes e JOIN collections c ON c.id=e.collection_id JOIN snapshots s ON s.sha256=e.source_hash WHERE ($1::text[] IS NULL OR e.id=ANY($1)) AND (NOT $2::boolean OR e.decision->>'published'='true' OR e.decision->'processed_video'->>'published'='true') AND (NOT $3 OR (e.decision->'processed_video' IS NULL AND EXISTS(SELECT 1 FROM episode_public_reviews p WHERE p.episode_id=e.id AND p.approved AND p.source_hash=e.source_hash AND p.file=e.decision->>'file'))) AND ($4::text[] IS NULL OR e.collection_id=ANY($4))`, ids, summaries, d.PublicOnly, collections)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]domain.Entry{}
	for rows.Next() {
		var e domain.Entry
		var s, b, subjects, languages []byte
		if err = rows.Scan(&s, &b, &e.Policy, &e.SourceHash, &e.Unavailable, &e.LocalPath, &e.LocalSHA256, &e.LocalFile, &e.LocalSnapshot, &e.Budget, &subjects, &languages); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(s, &e.Episode); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &e.Decision); err != nil {
			return nil, err
		}
		e.Episode.Subjects = domain.MetadataStrings(subjects)
		e.Episode.Languages = domain.MetadataStrings(languages)
		e.SourceTitle = e.Episode.Title
		if e.Decision.Title != "" {
			e.Episode.Title = e.Decision.Title
		}
		out[e.Episode.ID] = e
	}
	return out, rows.Err()
}
func (d *DB) Entries(ctx context.Context) (map[string]domain.Entry, error) {
	return d.entries(ctx, nil, false, nil)
}
func (d *DB) Summaries(ctx context.Context) (map[string]domain.Entry, error) {
	return d.entries(ctx, nil, true, nil)
}

// ScopedSummaries reads summaries only for the given collections; nil reads all.
func (d *DB) ScopedSummaries(ctx context.Context, collections []string) (map[string]domain.Entry, error) {
	return d.entries(ctx, nil, true, collections)
}
func (d *DB) Entry(ctx context.Context, id string) (domain.Entry, error) {
	entries, err := d.entries(ctx, []string{id}, false, nil)
	if err != nil {
		return domain.Entry{}, err
	}
	e, ok := entries[id]
	if !ok {
		return domain.Entry{}, pgx.ErrNoRows
	}
	return e, nil
}
func (d *DB) ScheduleEntries(ctx context.Context, s domain.Schedule) (map[string]domain.Entry, error) {
	ids := make([]string, 0, len(s.Programs))
	for _, p := range s.Programs {
		ids = append(ids, p.EpisodeID)
	}
	return d.entries(ctx, ids, false, nil)
}

// Channel polling needs the current asset, not every complete file inventory
// in a multi-day schedule. Full inventories remain retained in the database.
func (d *DB) CurrentEntries(ctx context.Context, s domain.Schedule, now time.Time) (map[string]domain.Entry, error) {
	state := s.At(now, nil)
	if state.Program == nil {
		return map[string]domain.Entry{}, nil
	}
	return d.entries(ctx, []string{state.Program.EpisodeID}, false, nil)
}

func (d *DB) ScheduleWithinCollection(ctx context.Context, s domain.Schedule, collection string) (bool, error) {
	if collection == "" {
		return true, nil
	}
	return d.ScheduleWithinScope(ctx, s, domain.CollectionScope{collection})
}
func (d *DB) ScheduleWithinScope(ctx context.Context, s domain.Schedule, scope domain.CollectionScope) (bool, error) {
	ids := make([]string, 0, len(s.Programs))
	for _, program := range s.Programs {
		ids = append(ids, program.EpisodeID)
	}
	var foreign bool
	err := d.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM unnest($1::text[]) requested(id) LEFT JOIN episodes e ON e.id=requested.id WHERE e.id IS NULL OR ($2::text[] IS NOT NULL AND NOT(e.collection_id=ANY($2))) OR ($3 AND NOT EXISTS(SELECT 1 FROM episode_public_reviews p WHERE p.episode_id=e.id AND p.approved AND p.source_hash=e.source_hash AND p.file=e.decision->>'file')))`, ids, []string(scope), d.PublicOnly).Scan(&foreign)
	return !foreign, err
}
func (d *DB) Decide(ctx context.Context, id string, decision domain.Decision) error {
	if decision.ProcessedVideo != nil {
		return fmt.Errorf("processed video requires verified private registration")
	}
	return d.decideVideo(ctx, id, decision, nil)
}

// ReviewProcessedVideo is called after the operator verifies original,
// derivative and receipts. Binding and original permissions are checked again
// under the episode lock, preserving every earlier decision in history.
func (d *DB) ReviewProcessedVideo(ctx context.Context, id string, review domain.ProcessedVideoReview) error {
	return d.decideVideo(ctx, id, domain.Decision{}, &review)
}

func (d *DB) decideVideo(ctx context.Context, id string, decision domain.Decision, processed *domain.ProcessedVideoReview) error {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var source, priorDecision []byte
	var e domain.Entry
	err = tx.QueryRow(ctx, `SELECT e.source,e.decision,c.policy,e.source_hash,c.budget,e.local_path,e.local_sha256,e.local_file,e.local_snapshot FROM episodes e JOIN collections c ON c.id=e.collection_id WHERE e.id=$1 FOR UPDATE OF e`, id).Scan(&source, &priorDecision, &e.Policy, &e.SourceHash, &e.Budget, &e.LocalPath, &e.LocalSHA256, &e.LocalFile, &e.LocalSnapshot)
	if err != nil {
		return err
	}
	if processed != nil {
		if err = json.Unmarshal(priorDecision, &decision); err != nil {
			return err
		}
		decision.ProcessedVideo = processed
	}
	if err = json.Unmarshal(source, &e.Episode); err != nil {
		return err
	}
	if err = e.ValidateDecision(decision); err != nil {
		return err
	}
	b, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE episodes SET decision=$2,updated_at=now() WHERE id=$1`, id, b); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO decision_history(episode_id,decision) VALUES($1,$2)`, id, b); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (d *DB) SaveSchedule(ctx context.Context, s domain.Schedule) error {
	if err := s.Validate(); err != nil {
		return err
	}
	entries, err := d.ScheduleEntries(ctx, s)
	if err != nil {
		return err
	}
	for _, p := range s.Programs {
		e, ok := entries[p.EpisodeID]
		f, eligible := e.Selected()
		if !ok || !eligible || !e.Watchable() || !e.ChannelApproved() || f.Name != p.File || p.Policy != e.Policy || p.Duration > f.Duration+0.1 {
			return fmt.Errorf("schedule references ineligible asset")
		}
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO schedules(revision,payload) VALUES($1,$2)`, s.Revision, b); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO current_schedule(id,revision) VALUES(1,$1) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision`, s.Revision); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (d *DB) Schedule(ctx context.Context) (domain.Schedule, error) {
	var s domain.Schedule
	var b []byte
	err := d.Pool.QueryRow(ctx, `SELECT payload FROM schedules JOIN current_schedule USING(revision) WHERE id=1`).Scan(&b)
	if err == nil {
		err = json.Unmarshal(b, &s)
	}
	return s, err
}

type WorkerState struct {
	CollectionID string     `json:"collection_id"`
	Page         int        `json:"page"`
	Complete     bool       `json:"discovery_complete"`
	Count        int64      `json:"source_count"`
	RetryAt      *time.Time `json:"retry_at"`
	LastSuccess  *time.Time `json:"last_success"`
	LastError    string     `json:"last_error"`
}

func (d *DB) State(ctx context.Context, id string) (WorkerState, error) {
	var s WorkerState
	err := d.Pool.QueryRow(ctx, `SELECT collection_id,page,discovery_complete,source_count,retry_at,last_success,last_error FROM worker_state WHERE collection_id=$1`, id).Scan(&s.CollectionID, &s.Page, &s.Complete, &s.Count, &s.RetryAt, &s.LastSuccess, &s.LastError)
	return s, err
}
func (d *DB) QueuePage(ctx context.Context, id string, page int, count int64, ids []string, complete bool) error {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, item := range ids {
		if !domain.ValidID(item) {
			return fmt.Errorf("invalid discovered item")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO metadata_queue(collection_id,item_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, item); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE worker_state SET page=$2,source_count=$3,discovery_complete=$4,last_error='',updated_at=now() WHERE collection_id=$1`, id, page, count, complete)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (d *DB) Pending(ctx context.Context, id string, n int) ([]string, error) {
	rows, err := d.Pool.Query(ctx, `SELECT item_id FROM metadata_queue WHERE collection_id=$1 AND state IN ('pending','retry') AND next_attempt<=now() ORDER BY discovered_at,item_id LIMIT $2`, id, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var item string
		if err = rows.Scan(&item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (d *DB) FinishItem(ctx context.Context, collection, item, state, msg string, retry time.Time, status int) error {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `UPDATE metadata_queue SET state=$3,error=$4,attempts=attempts+1,next_attempt=$5 WHERE collection_id=$1 AND item_id=$2`, collection, item, state, msg, retry)
	if err != nil {
		return err
	}
	if state == "unavailable" {
		if _, err = tx.Exec(ctx, `UPDATE episodes SET unavailable=true WHERE id=$1`, item); err != nil {
			return err
		}
	}
	if state == "imported" {
		if _, err = tx.Exec(ctx, `UPDATE episodes SET unavailable=false WHERE id=$1`, item); err != nil {
			return err
		}
	}
	if state != "imported" {
		if _, err = tx.Exec(ctx, `INSERT INTO observations(item_id,status,error) VALUES($1,$2,$3)`, item, status, msg); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (d *DB) Backoff(ctx context.Context, id string, until time.Time, msg string) error {
	_, err := d.Pool.Exec(ctx, `UPDATE worker_state SET retry_at=$2,last_error=$3,updated_at=now() WHERE collection_id=$1`, id, until, msg)
	return err
}
func (d *DB) Success(ctx context.Context, id string) error {
	_, err := d.Pool.Exec(ctx, `UPDATE worker_state SET retry_at=NULL,last_success=now(),last_error='',updated_at=now() WHERE collection_id=$1`, id)
	return err
}
func (d *DB) Reconcile(ctx context.Context, id string) error {
	_, err := d.Pool.Exec(ctx, `UPDATE worker_state SET page=1,discovery_complete=false WHERE collection_id=$1`, id)
	return err
}
func (d *DB) RefreshKnown(ctx context.Context, id string, limit int) error {
	_, err := d.Pool.Exec(ctx, `UPDATE metadata_queue SET state='pending',next_attempt=now() WHERE (collection_id,item_id) IN (SELECT collection_id,item_id FROM metadata_queue WHERE collection_id=$1 AND state IN ('imported','unavailable','rejected') ORDER BY next_attempt,item_id LIMIT $2)`, id, limit)
	return err
}
func (d *DB) Validators(ctx context.Context, id string) (string, string, error) {
	var e, m string
	err := d.Pool.QueryRow(ctx, `SELECT etag,modified FROM observations WHERE item_id=$1 AND snapshot_hash IS NOT NULL ORDER BY id DESC LIMIT 1`, id).Scan(&e, &m)
	if err == pgx.ErrNoRows {
		return "", "", nil
	}
	return e, m, err
}

// RetainedResponse supplies the body tied to a conditional source response.
// A rejected normalization may have a snapshot without an episode yet.
func (d *DB) RetainedResponse(ctx context.Context, id string) ([]byte, string, string, error) {
	var raw []byte
	var hash, etag, modified string
	err := d.Pool.QueryRow(ctx, `SELECT s.raw,s.sha256,o.etag,o.modified FROM observations o JOIN snapshots s ON s.sha256=o.snapshot_hash WHERE o.item_id=$1 ORDER BY o.id DESC LIMIT 1`, id).Scan(&raw, &hash, &etag, &modified)
	if err == nil && domain.Digest(raw) != hash {
		return nil, "", "", fmt.Errorf("retained metadata checksum mismatch")
	}
	return raw, etag, modified, err
}
func (d *DB) Counts(ctx context.Context, id string) (map[string]int64, error) {
	rows, err := d.Pool.Query(ctx, `SELECT state,count(*) FROM metadata_queue WHERE collection_id=$1 GROUP BY state`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var s string
		var n int64
		if err = rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		out[s] = n
	}
	return out, rows.Err()
}

// BindLocal records a verified immutable original only if its reviewed source identity still matches.
func (d *DB) BindLocal(ctx context.Context, id, snapshot, file, path, hash string, receipt []byte) error {
	if !json.Valid(receipt) || !filepath.IsLocal(path) || len(hash) != 64 {
		return fmt.Errorf("invalid preservation receipt")
	}
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current, policy string
	var decisionRaw []byte
	err = tx.QueryRow(ctx, `SELECT e.source_hash,c.policy,e.decision FROM episodes e JOIN collections c ON c.id=e.collection_id WHERE e.id=$1 FOR UPDATE OF e`, id).Scan(&current, &policy, &decisionRaw)
	if err != nil {
		return err
	}
	var decision domain.Decision
	if err = json.Unmarshal(decisionRaw, &decision); err != nil {
		return err
	}
	if policy != string(domain.Local) || current != snapshot || !decision.Acquisition || decision.File != file {
		return fmt.Errorf("review or source changed during acquisition")
	}
	if _, err = tx.Exec(ctx, `UPDATE episodes SET local_path=$2,local_sha256=$3,local_file=$4,local_snapshot=$5,updated_at=now() WHERE id=$1`, id, path, hash, file, snapshot); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO preservation_history(episode_id,receipt) VALUES($1,$2)`, id, json.RawMessage(receipt)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (d *DB) NotModified(ctx context.Context, id, etag, modified string) error {
	result, err := d.Pool.Exec(ctx, `INSERT INTO observations(item_id,snapshot_hash,status,etag,modified) SELECT item_id,snapshot_hash,304,COALESCE(NULLIF($2,''),etag),COALESCE(NULLIF($3,''),modified) FROM observations WHERE item_id=$1 AND snapshot_hash IS NOT NULL ORDER BY id DESC LIMIT 1`, id, etag, modified)
	if err == nil && result.RowsAffected() != 1 {
		return fmt.Errorf("304 has no retained snapshot")
	}
	return err
}

type Inventory struct {
	Episodes      int64 `json:"episodes"`
	Files         int64 `json:"files"`
	Published     int64 `json:"published"`
	Unavailable   int64 `json:"unavailable"`
	SnapshotBytes int64 `json:"raw_snapshot_bytes"`
	DatabaseBytes int64 `json:"database_bytes"`
	Snapshots     int64 `json:"snapshots"`
	Observations  int64 `json:"observations"`
}

func (d *DB) Inventory(ctx context.Context, id string) (Inventory, error) {
	var n Inventory
	err := d.Pool.QueryRow(ctx, `SELECT count(*),COALESCE(sum(jsonb_array_length(source->'files')),0),count(*) FILTER (WHERE decision->>'published'='true' OR decision->'processed_video'->>'published'='true'),count(*) FILTER (WHERE unavailable) FROM episodes WHERE collection_id=$1`, id).Scan(&n.Episodes, &n.Files, &n.Published, &n.Unavailable)
	if err != nil {
		return n, err
	}
	err = d.Pool.QueryRow(ctx, `SELECT count(*),COALESCE(sum(octet_length(raw)),0) FROM snapshots WHERE item_id=$1 OR item_id=(SELECT source_item FROM collections WHERE id=$1) OR item_id IN (SELECT item_id FROM metadata_queue WHERE collection_id=$1) OR item_id IN (SELECT id FROM episodes WHERE collection_id=$1)`, id).Scan(&n.Snapshots, &n.SnapshotBytes)
	if err != nil {
		return n, err
	}
	err = d.Pool.QueryRow(ctx, `SELECT count(*) FROM observations WHERE item_id=$1 OR item_id=(SELECT source_item FROM collections WHERE id=$1) OR item_id IN (SELECT item_id FROM metadata_queue WHERE collection_id=$1) OR item_id IN (SELECT id FROM episodes WHERE collection_id=$1)`, id).Scan(&n.Observations)
	if err != nil {
		return n, err
	}
	err = d.Pool.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&n.DatabaseBytes)
	return n, err
}

func (d *DB) LocalEntries(ctx context.Context, id string) (map[string]domain.Entry, error) {
	rows, err := d.Pool.Query(ctx, `SELECT e.id FROM episodes e JOIN collections c ON c.id=e.collection_id WHERE c.id=$1 AND c.policy='local_preserve' AND e.local_path<>''`, id)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return d.entries(ctx, ids, false, nil)
}
func (d *DB) PreservationReceipt(ctx context.Context, id string) ([]byte, error) {
	var raw []byte
	err := d.Pool.QueryRow(ctx, `SELECT receipt FROM preservation_history WHERE episode_id=$1 ORDER BY id DESC LIMIT 1`, id).Scan(&raw)
	return raw, err
}

func (d *DB) ItemEpisode(ctx context.Context, e domain.Episode, hash string) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if e.ID != "asset-"+domain.Digest([]byte(e.SourceItemID + "/" + e.SourceFile))[:24] {
		return fmt.Errorf("source file identity mismatch")
	}
	c, err := d.GetCollection(ctx, e.CollectionID)
	if err != nil {
		return err
	}
	if c.Policy != domain.Local || c.SourceItem == "" || c.SourceItem != e.SourceItemID {
		return fmt.Errorf("source-item episode policy mismatch")
	}
	var exists bool
	if err = d.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM snapshots WHERE sha256=$1 AND item_id=$2)`, hash, c.SourceItem).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("source snapshot missing")
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	result, err := d.Pool.Exec(ctx, `INSERT INTO episodes(id,collection_id,source_hash,source) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET source_hash=excluded.source_hash,source=excluded.source,unavailable=false,updated_at=now() WHERE episodes.collection_id=excluded.collection_id`, e.ID, e.CollectionID, hash, raw)
	if err == nil && result.RowsAffected() != 1 {
		return fmt.Errorf("episode already belongs to another collection")
	}
	return err
}

type CollectionSummary struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Policy    domain.Policy `json:"policy"`
	Episodes  int64         `json:"episodes"`
	SourceURL string        `json:"source_url"`
}

func (d *DB) Collections(ctx context.Context) ([]CollectionSummary, error) {
	rows, err := d.Pool.Query(ctx, `SELECT c.id,c.policy,count(e.id),COALESCE((SELECT parsed->'metadata'->>'title' FROM snapshots WHERE item_id=COALESCE(NULLIF(c.source_item,''),c.id) AND parsed->'metadata'->>'title' IS NOT NULL ORDER BY created_at DESC LIMIT 1),c.id),COALESCE(NULLIF(c.source_item,''),c.id) FROM collections c JOIN episodes e ON e.collection_id=c.id AND (e.decision->>'published'='true' OR e.decision->'processed_video'->>'published'='true') AND (NOT $1 OR (e.decision->'processed_video' IS NULL AND EXISTS(SELECT 1 FROM episode_public_reviews p WHERE p.episode_id=e.id AND p.approved AND p.source_hash=e.source_hash AND p.file=e.decision->>'file'))) GROUP BY c.id ORDER BY c.id`, d.PublicOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CollectionSummary{}
	for rows.Next() {
		var c CollectionSummary
		var source string
		if err = rows.Scan(&c.ID, &c.Policy, &c.Episodes, &c.Title, &source); err != nil {
			return nil, err
		}
		c.SourceURL = "https://archive.org/details/" + source
		out = append(out, c)
	}
	return out, rows.Err()
}
