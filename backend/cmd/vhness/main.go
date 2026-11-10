package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/app"
	audiomodule "git.subcult.tv/subculture-collective/vhness/internal/audio"
	"git.subcult.tv/subculture-collective/vhness/internal/catalog"
	"git.subcult.tv/subculture-collective/vhness/internal/community"
	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"git.subcult.tv/subculture-collective/vhness/internal/preservation"
	printmodule "git.subcult.tv/subculture-collective/vhness/internal/print"
	"git.subcult.tv/subculture-collective/vhness/internal/source"
	"git.subcult.tv/subculture-collective/vhness/internal/sourcecatalog"
	"git.subcult.tv/subculture-collective/vhness/internal/video"
	"git.subcult.tv/subculture-collective/vhness/internal/worker"
	"github.com/jackc/pgx/v5"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func load(p string, v any) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: vhness migrate|collection|review-budget|review-public-video|review-library|plan-replay|import|import-item|import-source|import-source-catalog|import-source-artist|set-source-catalog-listing|edit-source-catalog|import-source-popularity|review-source-audio|review-source-audio-batch|discover-catalog|refresh-catalog|import-catalog|prepare-curation|retain-legacy|review-transport|rebind-representation|import-labels|propose-labels|review-labels|index-print|restore-print-index|register-audio|register-video|schedule-audio|renew-public-audio|fetch|inspect|acquire|audit|review|schedule|discover|refresh|report|seed|serve|healthcheck")
	}
	cmd := os.Args[1]
	if cmd == "healthcheck" {
		address := strings.Replace(env("API_ADDR", "127.0.0.1:8080"), "0.0.0.0:", "127.0.0.1:", 1)
		client := &http.Client{Timeout: 4 * time.Second}
		response, err := client.Get("http://" + address + "/api/ready")
		if err != nil {
			return fmt.Errorf("readiness request failed")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("database not ready")
		}
		return nil
	}
	f := flag.NewFlagSet(cmd, flag.ContinueOnError)
	id := f.String("id", "", "source item identity")
	artist := f.String("artist", "", "exact recording-source artist name for a verified enrichment binding")
	representation := f.String("representation", "", "catalog representation identity")
	scope := os.Getenv("VHNESS_COLLECTION")
	allowlist, err := domain.ParseCollectionScope(scope, os.Getenv("VHNESS_COLLECTIONS"))
	if err != nil {
		return err
	}
	collection := f.String("collection", allowlist.Default(), "collection identity; explicit for a multi-collection operator action")
	file := f.String("file", "", "JSON input")
	sourceItem := f.String("source-item", "", "explicit multi-file source movie item")
	episodeFile := f.String("episode-file", "", "exact file to materialize from a selected source item")
	synthetic := f.Bool("synthetic", false, "explicit generated local fixture metadata")
	fixtureOriginal := f.String("fixture-original", "", "generated fixture original, only for vhness-local-fixture")
	policy := f.String("policy", string(domain.Stream), "delivery policy")
	budget := f.Int64("budget", 0, "local byte budget")
	reserve := f.Int64("reserve", 0, "free-space reserve")
	expectedBudget := f.Int64("expected-budget", 0, "previous reviewed budget required for explicit increase")
	expectedReserve := f.Int64("expected-reserve", 0, "previous reviewed reserve required for explicit increase")
	notes := f.String("notes", "", "authorization notes for explicit budget review")
	approvePublic := f.Bool("approve-public", false, "explicit owner-authorized publication; false revokes video publication")
	after := f.String("after-id", "", "bounded library review cursor")
	days := f.Int("days", 7, "explicit replay horizon, 1 through 7 days")
	hours := f.Int("hours", 168, "public audio renewal horizon, 1 through 168 hours")
	pages := f.Int("pages", 1, "bounded discovery pages (0–100)")
	items := f.Int("items", 20, "bounded metadata fetches (0–1000)")
	delay := f.Duration("delay", time.Second, "minimum interval between metadata-only catalog requests")
	timeout := f.Duration("timeout", 10*time.Minute, "run deadline")
	if err := f.Parse(os.Args[2:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	publicValue := os.Getenv("ARKHIV_PUBLIC")
	if publicValue != "" && publicValue != "0" && publicValue != "1" {
		return fmt.Errorf("ARKHIV_PUBLIC must be 0 or 1")
	}
	if publicValue == "1" && (cmd != "serve" || len(allowlist) == 0) {
		return fmt.Errorf("public mode requires serve and an explicit nonempty collection scope")
	}
	if *collection != "" && !allowlist.Allows(*collection) {
		return fmt.Errorf("collection outside configured instance scope")
	}
	switch cmd {
	case "migrate", "serve", "import-catalog", "schedule", "acquire", "review", "review-public-video", "inspect", "seed":
	default:
		if !allowlist.Allows(*collection) {
			return fmt.Errorf("select a collection inside configured scope")
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if cmd == "serve-session" {
		return serveSession(ctx)
	}
	if strings.HasPrefix(cmd, "community-") {
		return communityCommand(ctx, cmd, *file)
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if path := os.Getenv("DATABASE_URL_FILE"); path != "" {
		secret, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("database connection file unavailable")
		}
		databaseURL = strings.TrimSpace(string(secret))
	}
	d, err := catalog.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer d.Pool.Close()
	d.PublicOnly = publicValue == "1"
	if allowlist != nil && (cmd == "review" || cmd == "review-public-video" || cmd == "register-video" || cmd == "inspect") {
		entry, err := d.Entry(ctx, *id)
		if err != nil || !allowlist.Allows(entry.Episode.CollectionID) {
			return fmt.Errorf("episode outside configured instance scope")
		}
	}
	switch cmd {
	case "retain-legacy":
		input, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer input.Close()
		raw, err := io.ReadAll(io.LimitReader(input, (256<<20)+1))
		if err != nil {
			return err
		}
		return d.RetainLegacy(ctx, raw, *collection, *notes)
	case "prepare-curation":
		input, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer input.Close()
		raw, err := io.ReadAll(io.LimitReader(input, (16<<20)+1))
		if err != nil {
			return err
		}
		sha, err := d.PrepareCuration(ctx, raw, allowlist, *notes)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"draft_sha256": sha, "state": "operator-review", "public": false, "transport_granted": false})
	case "discover-catalog", "refresh-catalog", "import-source-artist", "set-source-catalog-listing", "import-source-catalog", "edit-source-catalog", "import-source-popularity", "review-source-audio", "review-source-audio-batch":
		store := &sourcecatalog.Store{Core: d, Collection: *collection}
		if err = store.Init(ctx); err != nil {
			return err
		}
		switch cmd {
		case "import-source-artist":
			input, e := os.Open(*file)
			if e != nil {
				return e
			}
			defer input.Close()
			var bundle sourcecatalog.ArtistBundle
			decoder := json.NewDecoder(io.LimitReader(input, (48<<20)+1))
			decoder.DisallowUnknownFields()
			if e = decoder.Decode(&bundle); e != nil {
				return e
			}
			var extra any
			if e = decoder.Decode(&extra); e != io.EOF {
				return fmt.Errorf("expected one artist bundle")
			}
			return store.ImportArtist(ctx, *artist, bundle, *notes)
		case "import-source-popularity":
			input, e := os.Open(*file)
			if e != nil {
				return e
			}
			defer input.Close()
			batch, e := sourcecatalog.DecodePopularityBatch(input)
			if e != nil {
				return e
			}
			report, e := store.ImportPopularity(ctx, batch)
			if e != nil {
				return e
			}
			return json.NewEncoder(os.Stdout).Encode(report)
		case "set-source-catalog-listing":
			input, e := os.Open(*file)
			if e != nil {
				return e
			}
			defer input.Close()
			var decision sourcecatalog.ListingDecision
			decoder := json.NewDecoder(io.LimitReader(input, (256<<10)+1))
			decoder.DisallowUnknownFields()
			if e = decoder.Decode(&decision); e != nil {
				return e
			}
			var extra any
			if e = decoder.Decode(&extra); e != io.EOF {
				return fmt.Errorf("expected one listing decision object")
			}
			return store.SetListing(ctx, decision, *notes)
		case "import-source-catalog":
			input, e := os.Open(*file)
			if e != nil {
				return e
			}
			defer input.Close()
			raw, e := io.ReadAll(io.LimitReader(input, (32<<20)+1))
			if e != nil {
				return e
			}
			return store.Import(ctx, *id, raw, 200, "", "")
		case "edit-source-catalog":
			input, e := os.Open(*file)
			if e != nil {
				return e
			}
			defer input.Close()
			raw, e := io.ReadAll(io.LimitReader(input, (256<<10)+1))
			if e != nil {
				return e
			}
			if len(raw) > 256<<10 {
				return fmt.Errorf("metadata correction exceeds input limit")
			}
			var patch map[string]any
			if e = json.Unmarshal(raw, &patch); e != nil {
				return e
			}
			return store.Edit(ctx, *id, patch, *notes)
		case "review-source-audio":
			return store.ReviewAudio(ctx, *id, *notes)
		case "review-source-audio-batch":
			if *timeout <= 0 || *timeout > 30*time.Minute {
				return fmt.Errorf("invalid playback review deadline")
			}
			deadline, cancel := context.WithTimeout(ctx, *timeout)
			defer cancel()
			coverage, e := store.ReviewAudioBatch(deadline, *items, *notes)
			if e != nil {
				return e
			}
			return json.NewEncoder(os.Stdout).Encode(coverage)
		case "refresh-catalog":
			if err = store.Reconcile(ctx); err != nil {
				return err
			}
		}
		if *timeout <= 0 || *timeout > 30*time.Minute {
			return fmt.Errorf("invalid catalog run deadline")
		}
		deadline, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()
		if err = store.Run(deadline, source.New(), *pages, *items, *delay); err != nil {
			return err
		}
		status, e := store.Status(ctx)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(status)
	case "import-source":
		if _, err := d.GetCollection(ctx, *collection); err != nil {
			return err
		}
		input, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer input.Close()
		raw, err := io.ReadAll(io.LimitReader(input, (32<<20)+1))
		if err != nil {
			return err
		}
		if err = source.CatalogSnapshot(*id, raw); err != nil {
			return err
		}
		return d.Snapshot(ctx, *id, raw, 200, "", "", nil)
	case "register-audio":
		var asset domain.AudioAsset
		if err := load(*file, &asset); err != nil {
			return err
		}
		return audiomodule.Register(ctx, d, os.Getenv("VHNESS_MEDIA"), os.Getenv("VHNESS_DERIVATIVES"), *id, *representation, *collection, asset, *notes)
	case "schedule-audio":
		var schedule domain.AudioSchedule
		if err := load(*file, &schedule); err != nil {
			return err
		}
		if schedule.Collection != *collection {
			return fmt.Errorf("audio schedule outside selected collection")
		}
		return d.StoreAudioSchedule(ctx, schedule, *notes)
	case "renew-public-audio":
		input, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer input.Close()
		raw, err := io.ReadAll(io.LimitReader(input, (64<<10)+1))
		if err != nil {
			return err
		}
		var selected []audiomodule.ScheduleSelection
		if len(raw) > 64<<10 || json.Unmarshal(raw, &selected) != nil {
			return fmt.Errorf("invalid bounded public audio selection")
		}
		schedule, err := audiomodule.RenewPublicSchedule(ctx, d, os.Getenv("VHNESS_MEDIA"), os.Getenv("VHNESS_DERIVATIVES"), *collection, selected, *hours, time.Now(), *notes)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(schedule)
	case "index-print":
		return printmodule.Index(ctx, d, os.Getenv("VHNESS_MEDIA"), os.Getenv("VHNESS_DERIVATIVES"), *id, *representation, *collection)
	case "restore-print-index":
		input, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer input.Close()
		raw, err := io.ReadAll(io.LimitReader(input, (32<<20)+1))
		if err != nil {
			return err
		}
		var index printmodule.RecoveryIndex
		if len(raw) > 32<<20 || json.Unmarshal(raw, &index) != nil {
			return fmt.Errorf("invalid bounded print recovery input")
		}
		return printmodule.RestoreIndex(ctx, d, os.Getenv("VHNESS_MEDIA"), os.Getenv("VHNESS_DERIVATIVES"), *id, *representation, *collection, index)
	case "import-catalog":
		var item domain.CatalogItem
		if err := load(*file, &item); err != nil {
			return err
		}
		if allowlist != nil {
			for _, c := range item.Collections {
				if !allowlist.Allows(c) {
					return fmt.Errorf("catalog mapping outside configured scope")
				}
			}
		}
		return d.InsertCatalog(ctx, item, *notes)
	case "review-transport":
		var input struct {
			Review       domain.TransportReview      `json:"review"`
			Preservation domain.PreservationApproval `json:"preservation"`
		}
		if err := load(*file, &input); err != nil {
			return err
		}
		if *approvePublic {
			return d.ReviewPublicTransport(ctx, *id, *representation, *collection, input.Review, input.Preservation)
		}
		return d.ReviewTransport(ctx, *id, *representation, *collection, input.Review, input.Preservation)
	case "rebind-representation":
		var input struct {
			Representation domain.Representation       `json:"representation"`
			Review         domain.TransportReview      `json:"review"`
			Preservation   domain.PreservationApproval `json:"preservation"`
		}
		if err := load(*file, &input); err != nil {
			return err
		}
		if input.Representation.ID != *representation {
			return fmt.Errorf("representation flag and input differ")
		}
		return d.RebindRepresentation(ctx, *id, input.Representation, *collection, input.Review, input.Preservation, *approvePublic, *notes)
	case "import-labels":
		var labels []domain.Label
		if err := load(*file, &labels); err != nil {
			return err
		}
		return d.ImportLabels(ctx, labels)
	case "propose-labels", "review-labels":
		var n int
		var err error
		if cmd == "propose-labels" {
			var proposals []domain.LabelProposal
			if err = load(*file, &proposals); err != nil {
				return err
			}
			n, err = d.ProposeLabels(ctx, proposals, *notes)
		} else {
			var decisions []domain.LabelDecision
			if err = load(*file, &decisions); err != nil {
				return err
			}
			n, err = d.ReviewLabels(ctx, decisions, *notes)
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]int{"changed": n})
	case "review-public-video":
		return d.ReviewPublicVideo(ctx, *id, *approvePublic, *notes)
	case "migrate":
		return d.Migrate(ctx, env("VHNESS_MIGRATIONS", "../migrations"))
	case "collection":
		return d.Collection(ctx, domain.Collection{ID: *collection, Policy: domain.Policy(*policy), Budget: *budget, Reserve: *reserve, SourceItem: *sourceItem})
	case "review-budget":
		return d.ReviewBudget(ctx, *collection, *expectedBudget, *expectedReserve, *budget, *reserve, *notes)
	case "review-library":
		if strings.TrimSpace(*notes) == "" || *items < 1 || *items > 1000 {
			return fmt.Errorf("bounded library review needs authorization notes and 1 through 1000 items")
		}
		c, err := d.GetCollection(ctx, *collection)
		if err != nil || c.Policy != domain.Stream {
			return fmt.Errorf("library review requires an existing source-streaming collection")
		}
		rows, err := d.Pool.Query(ctx, `SELECT id FROM episodes WHERE collection_id=$1 AND id>$2 AND coalesce(decision->>'notes','')='' ORDER BY id LIMIT $3`, *collection, *after, *items)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		selected := map[string]string{}
		skipped := map[string]string{}
		cursor := *after
		for _, id := range ids {
			cursor = id
			e, err := d.Entry(ctx, id)
			if err != nil {
				return err
			}
			if e.Unavailable {
				skipped[id] = "source unavailable"
				continue
			}
			files := []domain.File{}
			for _, file := range e.Episode.Files {
				if file.Candidate() {
					files = append(files, file)
				}
			}
			score := func(file domain.File) int {
				if file.Height == 480 {
					return 0
				}
				if file.Height > 0 && file.Height <= 1080 {
					return 1
				}
				return 2
			}
			sort.Slice(files, func(i, j int) bool {
				if score(files[i]) != score(files[j]) {
					return score(files[i]) < score(files[j])
				}
				if files[i].Bytes != files[j].Bytes {
					return files[i].Bytes < files[j].Bytes
				}
				return files[i].Name < files[j].Name
			})
			if len(files) == 0 {
				skipped[id] = "no eligible MP4 with a reported duration"
				continue
			}
			decision := domain.Decision{Published: true, Playback: true, Channel: true, File: files[0].Name, Notes: *notes + "; source snapshot " + e.SourceHash + "; private playback eligibility from source inventory, individual decoding unverified"}
			if err = d.Decide(ctx, id, decision); err != nil {
				return err
			}
			selected[id] = decision.File
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"reviewed": selected, "skipped": skipped, "after_id": cursor, "visited": len(ids)})
	case "plan-replay":
		if *days < 1 || *days > 7 {
			return fmt.Errorf("replay horizon must be 1 through 7 days")
		}
		rows, err := d.Pool.Query(ctx, `SELECT id FROM episodes WHERE collection_id=$1 AND decision->>'channel'='true' ORDER BY source->>'date' DESC,id LIMIT 500`, *collection)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		eligible := []domain.Entry{}
		for _, id := range ids {
			entry, err := d.Entry(ctx, id)
			if err != nil {
				return err
			}
			if _, err = domain.Resolve(entry, true); err == nil {
				file, _ := entry.Selected()
				entry.Episode.Files = []domain.File{file}
				eligible = append(eligible, entry)
			}
		}
		if len(eligible) == 0 {
			return fmt.Errorf("no reviewed channel-eligible episodes")
		}
		sort.Slice(eligible, func(i, j int) bool {
			a, b := eligible[i].Episode, eligible[j].Episode
			if a.SourceFile != "" && b.SourceFile != "" {
				ai, _ := strconv.Atoi(strings.Fields(a.SourceFile)[0])
				bi, _ := strconv.Atoi(strings.Fields(b.SourceFile)[0])
				if ai != bi {
					return ai < bi
				}
			}
			if a.Date != b.Date {
				return a.Date > b.Date
			}
			return a.ID < b.ID
		})
		now := time.Now().UTC()
		horizon := now.Add(time.Duration(*days) * 24 * time.Hour)
		planned := domain.Schedule{Revision: "replay-" + now.Format("20060102T150405.000000000Z")}
		existing, scheduleErr := d.Schedule(ctx)
		if scheduleErr != nil && !errors.Is(scheduleErr, pgx.ErrNoRows) {
			return scheduleErr
		}
		if scheduleErr == nil {
			for _, program := range existing.Programs {
				entry, entryErr := d.Entry(ctx, program.EpisodeID)
				if entryErr != nil || entry.Episode.CollectionID != *collection {
					return fmt.Errorf("existing schedule outside configured collection; review its replacement explicitly")
				}
				if program.Start.Add(time.Duration(program.Duration * float64(time.Second))).After(now) {
					planned.Programs = append(planned.Programs, program)
				}
			}
		}
		start := now
		index := 0
		if len(planned.Programs) > 0 {
			last := planned.Programs[len(planned.Programs)-1]
			start = last.Start.Add(time.Duration(last.Duration * float64(time.Second)))
			for i, entry := range eligible {
				if entry.Episode.ID == last.EpisodeID {
					index = (i + 1) % len(eligible)
					break
				}
			}
		}
		for start.Before(horizon) {
			if len(planned.Programs) >= 1000 {
				return fmt.Errorf("replay exceeds bounded program count; reduce horizon")
			}
			entry := eligible[index%len(eligible)]
			file, _ := entry.Selected()
			planned.Programs = append(planned.Programs, domain.Program{EpisodeID: entry.Episode.ID, File: file.Name, Policy: entry.Policy, Start: start, Duration: file.Duration})
			start = start.Add(time.Duration(file.Duration * float64(time.Second)))
			index++
		}
		if err = d.SaveSchedule(ctx, planned); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"revision": planned.Revision, "programs": len(planned.Programs), "horizon": start})
	case "import-item":
		c, err := d.GetCollection(ctx, *collection)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		e, err := source.ItemFile(c, *id, *episodeFile, raw)
		if err != nil {
			return err
		}
		if err = d.Snapshot(ctx, *id, raw, 200, "", "", nil); err != nil {
			return err
		}
		if err = d.ItemEpisode(ctx, e, domain.Digest(raw)); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"episode_id": e.ID, "source_item": e.SourceItemID, "file": *episodeFile})
	case "import":
		raw, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		e, err := source.Normalize(*id, *collection, raw)
		if err != nil {
			return err
		}
		if *synthetic {
			if *collection != "vhness-local-fixture" {
				return fmt.Errorf("synthetic import requires vhness-local-fixture")
			}
			e.Synthetic = true
		}
		return d.Snapshot(ctx, *id, raw, 200, "", "", &e)
	case "fetch":
		response, err := source.New().Metadata(ctx, *id, "", "")
		if err != nil {
			return err
		}
		e, err := source.Normalize(*id, *collection, response.Raw)
		if err != nil {
			return err
		}
		return d.Snapshot(ctx, *id, response.Raw, response.Status, response.ETag, response.Modified, &e)
	case "audit":
		c, err := d.GetCollection(ctx, *collection)
		if err != nil {
			return err
		}
		if c.Policy != domain.Local {
			return fmt.Errorf("integrity audit requires a local collection")
		}
		entries, err := d.LocalEntries(ctx, *collection)
		if err != nil {
			return err
		}
		failures := 0
		results := map[string]string{}
		for id, e := range entries {
			raw, checkErr := d.PreservationReceipt(ctx, id)
			var receipt preservation.Receipt
			if checkErr == nil {
				checkErr = json.Unmarshal(raw, &receipt)
			}
			if checkErr == nil && (receipt.Path != e.LocalPath || receipt.SHA256 != e.LocalSHA256 || receipt.Manifest.Snapshot != e.LocalSnapshot || receipt.Manifest.File != e.LocalFile || receipt.Manifest.ID != id) {
				checkErr = fmt.Errorf("preservation lineage mismatch")
			}
			if checkErr == nil {
				f, verifyErr := preservation.Verify(os.Getenv("VHNESS_MEDIA"), e.LocalPath, e.LocalSHA256, receipt.Manifest.Bytes)
				checkErr = verifyErr
				if f != nil {
					f.Close()
				}
			}
			if checkErr != nil {
				failures++
				results[id] = checkErr.Error()
			} else {
				results[id] = "verified"
			}
		}
		if err = json.NewEncoder(os.Stdout).Encode(map[string]any{"originals": len(entries), "failures": failures, "results": results}); err != nil {
			return err
		}
		if failures > 0 {
			return fmt.Errorf("integrity audit failed for %d originals", failures)
		}
		return nil
	case "acquire":
		if *timeout <= 0 || *timeout > 30*time.Minute {
			return fmt.Errorf("acquisition deadline must be within 30 minutes")
		}
		var manifest preservation.Manifest
		if err = load(*file, &manifest); err != nil {
			return err
		}
		if !allowlist.Allows(manifest.Collection) {
			return fmt.Errorf("manifest outside configured instance scope")
		}
		c, err := d.GetCollection(ctx, manifest.Collection)
		if err != nil {
			return err
		}
		e, err := d.Entry(ctx, manifest.ID)
		if err != nil {
			return err
		}
		acquisitionCtx, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()
		mediaRoot := os.Getenv("VHNESS_MEDIA")
		if !filepath.IsAbs(mediaRoot) {
			return fmt.Errorf("absolute VHNESS_MEDIA required")
		}
		collectionRoot := filepath.Join(mediaRoot, c.ID)
		acquirer := preservation.New(collectionRoot)
		if *fixtureOriginal != "" {
			acquirer = preservation.GeneratedFixture(collectionRoot, *fixtureOriginal)
		}
		receipt, err := acquirer.Acquire(acquisitionCtx, c, e, manifest)
		if err != nil {
			return err
		}
		receipt.Path = filepath.Join(c.ID, receipt.Path)
		raw, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		if err = d.BindLocal(ctx, manifest.ID, manifest.Snapshot, manifest.File, receipt.Path, receipt.SHA256, raw); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(receipt)
	case "review":
		var decision domain.Decision
		if err := load(*file, &decision); err != nil {
			return err
		}
		return d.Decide(ctx, *id, decision)
	case "register-video":
		var review domain.ProcessedVideoReview
		if err := load(*file, &review); err != nil {
			return err
		}
		return video.Register(ctx, d, os.Getenv("VHNESS_MEDIA"), os.Getenv("VHNESS_VIDEO_DERIVATIVES"), os.Getenv("VHNESS_VIDEO_RECEIPTS"), *id, review)
	case "schedule":
		var s domain.Schedule
		if err := load(*file, &s); err != nil {
			return err
		}
		if allowlist != nil {
			for _, program := range s.Programs {
				entry, err := d.Entry(ctx, program.EpisodeID)
				if err != nil || !allowlist.Allows(entry.Episode.CollectionID) {
					return fmt.Errorf("scheduled episode outside configured instance scope")
				}
			}
		}
		return d.SaveSchedule(ctx, s)
	case "discover", "refresh":
		if c, collectionErr := d.GetCollection(ctx, *collection); collectionErr != nil {
			return collectionErr
		} else if c.SourceItem != "" {
			return fmt.Errorf("file-based sources require explicit import-item selection")
		}
		if _, err = d.GetCollection(ctx, *collection); err != nil {
			return err
		}
		if cmd == "refresh" {
			if err = d.Reconcile(ctx, *collection); err != nil {
				return err
			}
			if err = d.RefreshKnown(ctx, *collection, *items); err != nil {
				return err
			}
		}
		client := source.New()
		w := worker.Worker{DB: d, Source: client}
		result, err := w.Run(ctx, *collection, *pages, *items, *timeout)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	case "report":
		entries, err := d.Entries(ctx)
		if err != nil {
			return err
		}
		state, err := d.State(ctx, *collection)
		if err != nil {
			return err
		}
		counts, err := d.Counts(ctx, *collection)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"episodes": len(entries), "worker": state, "counts": counts})
	case "inspect":
		entries, err := d.Entries(ctx)
		if err != nil {
			return err
		}
		e, ok := entries[*id]
		if !ok {
			return fmt.Errorf("episode not found")
		}
		return json.NewEncoder(os.Stdout).Encode(e)
	case "seed":
		if !allowlist.Allows(domain.DemocracyNow) {
			return fmt.Errorf("synthetic seed requires Democracy Now! instance scope")
		}
		if err = d.Collection(ctx, domain.Collection{ID: domain.DemocracyNow, Policy: domain.Stream}); err != nil {
			return err
		}
		for i := 1; i <= 3; i++ {
			item := fmt.Sprintf("vhness-synthetic-%d", i)
			payload := map[string]any{"metadata": map[string]any{"identifier": item, "mediatype": "movies", "collection": []string{domain.DemocracyNow}, "title": fmt.Sprintf("Synthetic episode %02d — development fixture", i), "date": fmt.Sprintf("2026-10-%02d", i), "description": "Clearly labeled synthetic metadata. No video is stored or acquired."}, "files": []map[string]any{{"name": "synthetic.mp4", "format": "h.264", "size": "1", "length": "60"}}}
			raw, _ := json.Marshal(payload)
			e, err := source.Normalize(item, domain.DemocracyNow, raw)
			if err != nil {
				return err
			}
			e.Synthetic = true
			if err = d.Snapshot(ctx, item, raw, 200, "", "", &e); err != nil {
				return err
			}
			if err = d.Decide(ctx, item, domain.Decision{Published: true, Playback: true, Channel: true, File: "synthetic.mp4", Notes: "Synthetic metadata fixture; no real media"}); err != nil {
				return err
			}
		}
		return nil
	case "serve":
		personal, err := community.Load(ctx, os.Getenv("ARKHIV_COMMUNITY_CONFIG_FILE"))
		if err != nil {
			return err
		}
		if personal != nil {
			defer personal.Pool.Close()
		}
		compatibility := os.Getenv("ARKHIV_COMPATIBILITY")
		if compatibility != "" && compatibility != "print" && compatibility != "audio" {
			return fmt.Errorf("ARKHIV_COMPATIBILITY must be print or audio")
		}
		if compatibility != "" && len(allowlist) != 1 {
			return fmt.Errorf("compatibility API requires exactly one collection")
		}
		server := &http.Server{Addr: env("API_ADDR", "127.0.0.1:8080"), Handler: (&app.Server{DB: d, Community: personal, Compatibility: compatibility, Revision: os.Getenv("ARKHIV_REVISION"), Web: os.Getenv("VHNESS_WEB"), Media: os.Getenv("VHNESS_MEDIA"), Derivatives: os.Getenv("VHNESS_DERIVATIVES"), VideoDerivatives: os.Getenv("VHNESS_VIDEO_DERIVATIVES"), VideoReceipts: os.Getenv("VHNESS_VIDEO_RECEIPTS"), Collection: scope, Collections: allowlist}).Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdown)
		}()
		log.Printf("VHNESS API listening on %s; data root %s", server.Addr, filepath.Clean(env("VHNESS_DATA", "../var")))
		err = server.ListenAndServe()
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}
func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
