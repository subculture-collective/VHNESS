package sourcecatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"github.com/jackc/pgx/v5"
)

// Application bounds from the popularity coverage decision. They are not
// claimed provider limits.
const (
	popularityBatchLimit    = 50
	popularityResponseLimit = 1 << 20
	popularityFreshFor      = 24 * time.Hour
	popularityReadLimit     = 1000
)

var popularityProvider = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
var wholeNumber = regexp.MustCompile(`^[0-9]+$`)

// PopularityBatch is one dated provider response and the identifiers it asked
// for. Response holds the exact bytes, even when they cannot be parsed.
type PopularityBatch struct {
	Provider   string    `json:"provider"`
	ObservedAt time.Time `json:"observed_at"`
	Requested  []string  `json:"requested"`
	Response   []byte    `json:"response_base64"`
}

// PopularityReport says what an import did with each requested identifier.
type PopularityReport struct {
	ResponseValid  bool   `json:"response_valid"`
	ResponseSHA256 string `json:"response_sha256"`
	Requested      int    `json:"requested"`
	Usable         int    `json:"usable"`
	NoData         int    `json:"no_data"`
	Malformed      int    `json:"malformed"`
	Omitted        int    `json:"omitted"`
	UnknownItems   int    `json:"unknown_items"`
}

// Popularity is the retained state for one recording. Counts are nil when the
// provider reported no data. Fresh is false for anything older than a day, so
// callers can keep a stale record as a candidate without a popularity claim.
type Popularity struct {
	Identifier  string    `json:"identifier"`
	HaveData    bool      `json:"have_data"`
	AllTime     *int64    `json:"all_time"`
	Last30Day   *int64    `json:"last_30day"`
	Last7Day    *int64    `json:"last_7day"`
	ObservedAt  time.Time `json:"observed_at"`
	Fresh       bool      `json:"fresh"`
	LastOutcome string    `json:"last_outcome"`
}

// DecodePopularityBatch reads one bounded operator batch.
func DecodePopularityBatch(input io.Reader) (PopularityBatch, error) {
	var batch PopularityBatch
	decoder := json.NewDecoder(io.LimitReader(input, popularityResponseLimit*2))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&batch); err != nil {
		return batch, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return batch, fmt.Errorf("expected one popularity batch")
	}
	return batch, nil
}

func (b PopularityBatch) validate(now time.Time) error {
	if !popularityProvider.MatchString(b.Provider) {
		return fmt.Errorf("use a lowercase provider name")
	}
	if len(b.Requested) < 1 || len(b.Requested) > popularityBatchLimit {
		return fmt.Errorf("a batch names 1 to %d identifiers", popularityBatchLimit)
	}
	for _, id := range b.Requested {
		if !ItemID.MatchString(id) {
			return fmt.Errorf("invalid source identity %q", id)
		}
	}
	if len(b.Response) > popularityResponseLimit {
		return fmt.Errorf("provider response exceeds %d bytes", popularityResponseLimit)
	}
	if b.ObservedAt.IsZero() || b.ObservedAt.After(now.Add(5*time.Minute)) {
		return fmt.Errorf("use the actual observation time")
	}
	return nil
}

type viewCounts struct {
	haveData bool
	allTime  int64
	days30   int64
	days7    int64
}

func counter(entry map[string]any, field string) (int64, bool) {
	number, ok := entry[field].(json.Number)
	if !ok || !wholeNumber.MatchString(number.String()) {
		return 0, false
	}
	value, err := strconv.ParseInt(number.String(), 10, 64)
	return value, err == nil
}

// A counter must be a nonnegative whole JSON number that fits the column.
func parseViews(raw json.RawMessage) (viewCounts, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var entry map[string]any
	if err := decoder.Decode(&entry); err != nil || entry == nil {
		return viewCounts{}, false
	}
	have, ok := entry["have_data"].(bool)
	if !ok {
		return viewCounts{}, false
	}
	if !have {
		return viewCounts{}, true
	}
	all, okAll := counter(entry, "all_time")
	days30, ok30 := counter(entry, "last_30day")
	days7, ok7 := counter(entry, "last_7day")
	if !okAll || !ok30 || !ok7 {
		return viewCounts{}, false
	}
	return viewCounts{haveData: true, allTime: all, days30: days30, days7: days7}, true
}

// ImportPopularity keeps the exact response, then applies each requested
// identifier on its own. A malformed or omitted entry never replaces earlier
// counts, and no recording metadata or editorial history is touched.
func (s *Store) ImportPopularity(ctx context.Context, batch PopularityBatch) (PopularityReport, error) {
	report := PopularityReport{}
	if err := batch.validate(time.Now()); err != nil {
		return report, err
	}
	requested := append([]string(nil), batch.Requested...)
	sort.Strings(requested)
	unique := requested[:0]
	for _, id := range requested {
		if len(unique) == 0 || unique[len(unique)-1] != id {
			unique = append(unique, id)
		}
	}
	report.Requested = len(unique)
	report.ResponseSHA256 = domain.Digest(batch.Response)
	var entries map[string]json.RawMessage
	report.ResponseValid = json.Unmarshal(batch.Response, &entries) == nil && entries != nil

	tx, err := s.Core.Pool.Begin(ctx)
	if err != nil {
		return report, err
	}
	defer tx.Rollback(ctx)
	requestedJSON, _ := json.Marshal(unique)
	var responseID int64
	err = tx.QueryRow(ctx, `WITH added AS (
 INSERT INTO source_popularity_responses(provider,sha256,observed_at,requested,raw,valid) VALUES($1,$2,$3,$4,$5,$6)
 ON CONFLICT(provider,sha256,observed_at) DO NOTHING RETURNING id)
 SELECT id FROM added UNION ALL SELECT id FROM source_popularity_responses WHERE provider=$1 AND sha256=$2 AND observed_at=$3 LIMIT 1`,
		batch.Provider, report.ResponseSHA256, batch.ObservedAt, requestedJSON, batch.Response, report.ResponseValid).Scan(&responseID)
	if err != nil {
		return report, err
	}
	rows, err := tx.Query(ctx, `SELECT identifier FROM source_catalog_items WHERE collection_id=$1 AND identifier=ANY($2)`, s.Collection, unique)
	if err != nil {
		return report, err
	}
	known, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return report, err
	}
	present := map[string]bool{}
	for _, id := range known {
		present[id] = true
	}
	for _, id := range unique {
		if !present[id] {
			report.UnknownItems++
			continue
		}
		raw, listed := entries[id]
		outcome := "omitted"
		counts := viewCounts{}
		if !report.ResponseValid {
			outcome = "malformed"
		} else if listed {
			parsed, valid := parseViews(raw)
			switch {
			case !valid:
				outcome = "malformed"
			case parsed.haveData:
				outcome, counts = "usable", parsed
			default:
				outcome = "no_data"
			}
		}
		switch outcome {
		case "usable":
			report.Usable++
			_, err = tx.Exec(ctx, `INSERT INTO source_popularity_snapshots AS current(collection_id,identifier,provider,have_data,all_time,last_30day,last_7day,observed_at,response_id,last_attempted_at,last_outcome)
 VALUES($1,$2,$3,true,$4,$5,$6,$7,$8,$7,'usable') ON CONFLICT(collection_id,identifier,provider) DO UPDATE SET
 have_data=true,all_time=excluded.all_time,last_30day=excluded.last_30day,last_7day=excluded.last_7day,observed_at=excluded.observed_at,response_id=excluded.response_id,last_attempted_at=excluded.last_attempted_at,last_outcome='usable'
 WHERE current.last_attempted_at<=excluded.last_attempted_at`, s.Collection, id, batch.Provider, counts.allTime, counts.days30, counts.days7, batch.ObservedAt, responseID)
		case "no_data":
			report.NoData++
			_, err = tx.Exec(ctx, `INSERT INTO source_popularity_snapshots AS current(collection_id,identifier,provider,have_data,observed_at,response_id,last_attempted_at,last_outcome)
 VALUES($1,$2,$3,false,$4,$5,$4,'no_data') ON CONFLICT(collection_id,identifier,provider) DO UPDATE SET
 observed_at=CASE WHEN current.have_data THEN current.observed_at ELSE excluded.observed_at END,
 response_id=CASE WHEN current.have_data THEN current.response_id ELSE excluded.response_id END,
 last_attempted_at=excluded.last_attempted_at,last_outcome='no_data'
 WHERE current.last_attempted_at<=excluded.last_attempted_at`, s.Collection, id, batch.Provider, batch.ObservedAt, responseID)
		default:
			if outcome == "malformed" {
				report.Malformed++
			} else {
				report.Omitted++
			}
			_, err = tx.Exec(ctx, `UPDATE source_popularity_snapshots SET last_attempted_at=$4,last_outcome=$5
 WHERE collection_id=$1 AND identifier=$2 AND provider=$3 AND last_attempted_at<=$4`, s.Collection, id, batch.Provider, batch.ObservedAt, outcome)
		}
		if err != nil {
			return report, err
		}
	}
	return report, tx.Commit(ctx)
}

// Popularity returns retained state for the identifiers that have any. Page
// reads use this data only and never contact the provider.
func (s *Store) Popularity(ctx context.Context, provider string, identifiers []string) ([]Popularity, error) {
	if !popularityProvider.MatchString(provider) || len(identifiers) > popularityReadLimit {
		return nil, fmt.Errorf("supply a provider and at most %d identifiers", popularityReadLimit)
	}
	rows, err := s.Core.Pool.Query(ctx, `SELECT identifier,have_data,all_time,last_30day,last_7day,observed_at,have_data AND observed_at>=now()-$4::interval,last_outcome
 FROM source_popularity_snapshots WHERE collection_id=$1 AND provider=$2 AND identifier=ANY($3) ORDER BY identifier`,
		s.Collection, provider, identifiers, popularityFreshFor.String())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Popularity, error) {
		var p Popularity
		err := row.Scan(&p.Identifier, &p.HaveData, &p.AllTime, &p.Last30Day, &p.Last7Day, &p.ObservedAt, &p.Fresh, &p.LastOutcome)
		return p, err
	})
}
