package audio

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/catalog"
	"git.subcult.tv/subculture-collective/vhness/internal/domain"
)

type ScheduleSelection struct {
	Item           string `json:"item_id"`
	Representation string `json:"representation_id"`
}

// RenewPublicSchedule retains the operator's exact selection and repeats only
// currently approved, verified recordings. It never discovers or acquires media.
func RenewPublicSchedule(ctx context.Context, db *catalog.DB, mediaRoot, derivativeRoot, collection string, selected []ScheduleSelection, hours int, now time.Time, notes string) (domain.AudioSchedule, error) {
	var schedule domain.AudioSchedule
	if len(selected) < 1 || len(selected) > 12 || hours < 1 || hours > 168 || strings.TrimSpace(notes) == "" {
		return schedule, fmt.Errorf("public renewal requires 1-12 explicit recordings, 1-168 hours, and notes")
	}
	public := *db
	public.PublicOnly = true
	durations := make([]float64, len(selected))
	seen := map[ScheduleSelection]bool{}
	for i, selection := range selected {
		if seen[selection] {
			return schedule, fmt.Errorf("duplicate renewal recording")
		}
		seen[selection] = true
		entry, err := public.Transport(ctx, selection.Item, selection.Representation, collection)
		if err != nil {
			return schedule, err
		}
		asset, err := public.AudioAsset(ctx, selection.Item, selection.Representation, collection)
		if err != nil {
			return schedule, err
		}
		f, err := Verify(nil, mediaRoot, derivativeRoot, entry, asset, domain.ScheduleAction)
		if err != nil {
			return schedule, err
		}
		f.Close()
		durations[i] = asset.Duration
	}
	start := now.UTC().Truncate(time.Hour)
	end := start.Add(time.Duration(hours) * time.Hour)
	schedule.Collection = collection
	schedule.Programs = []domain.AudioProgram{}
	for i := 0; start.Before(end); i++ {
		if len(schedule.Programs) >= 1000 {
			return domain.AudioSchedule{}, fmt.Errorf("renewal exceeds program bound")
		}
		selection := selected[i%len(selected)]
		duration := durations[i%len(selected)]
		schedule.Programs = append(schedule.Programs, domain.AudioProgram{Item: selection.Item, Representation: selection.Representation, Start: start, Duration: duration})
		start = start.Add(time.Duration(duration * float64(time.Second)))
	}
	data, _ := json.Marshal(schedule.Programs)
	schedule.Revision = "public-" + now.UTC().Truncate(time.Hour).Format("20060102T150405Z") + "-" + domain.Digest(data)[:12]
	// A repeat within the same hour retains the exact original timeline.
	previous, err := public.AudioSchedule(ctx, collection)
	if err == nil && previous.Revision == schedule.Revision {
		return previous, nil
	}
	if err = public.StoreAudioSchedule(ctx, schedule, notes); err != nil {
		return domain.AudioSchedule{}, err
	}
	return schedule, nil
}
