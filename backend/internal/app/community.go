package app

import (
	"context"
	"errors"
	"fmt"
	"git.subcult.tv/subculture-collective/vhness/internal/catalog"
	"strings"
	"time"

	audiomodule "git.subcult.tv/subculture-collective/vhness/internal/audio"
	"git.subcult.tv/subculture-collective/vhness/internal/community"
	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"github.com/jackc/pgx/v5"
)

func (s *Server) communityCurrentMedia(ctx context.Context, collection, media string) (*community.Program, error) {
	if media == "video" {
		return s.communityCurrent(ctx, collection)
	}
	if media != "audio" || !s.allowsCollection(collection) {
		return nil, fmt.Errorf("collection unavailable")
	}
	schedule, err := s.DB.AudioSchedule(ctx, collection)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if schedule.Validate() != nil || schedule.Collection != collection {
		return nil, nil
	}
	now := time.Now().UTC()
	if s.Clock != nil {
		now = s.Clock().UTC()
	}
	var current *community.Program
	for _, program := range schedule.Programs {
		entry, err := s.DB.Transport(ctx, program.Item, program.Representation, collection)
		if err != nil {
			return nil, nil
		}
		asset, err := s.DB.AudioAsset(ctx, program.Item, program.Representation, collection)
		if err != nil || domain.AuthorizeAudioAsset(entry.Collection, entry.Representation, entry.Raw, entry.Review, entry.Preservation, asset, domain.ScheduleAction) != nil || program.Duration > asset.Duration {
			return nil, nil
		}
		offset := now.Sub(program.Start).Seconds()
		if offset >= 0 && offset < program.Duration {
			file, err := audiomodule.Verify(s.Media, s.Derivatives, entry, asset, domain.ScheduleAction)
			if err != nil {
				return nil, nil
			}
			file.Close()
			current = &community.Program{Media: "audio", Revision: schedule.Revision, Start: program.Start, Item: program.Item, Representation: program.Representation, OriginalDate: entry.Representation.Extension.Audio.Recording.Date}
		}
	}
	return current, nil
}

func (s *Server) communityAvailable(ctx context.Context, collection, id string) bool {
	if !s.allowsCollection(collection) {
		return false
	}
	if strings.HasPrefix(id, "catalog:") {
		page, err := s.DB.Catalog(ctx, catalog.CatalogQuery{Scope: []string{collection}, Collection: collection, ID: strings.TrimPrefix(id, "catalog:"), Page: 1})
		return err == nil && len(page.Items) == 1
	}
	entry, err := s.DB.Entry(ctx, id)
	if err != nil || entry.Episode.CollectionID != collection || !entry.Published() || entry.Unavailable {
		return false
	}
	_, err = domain.Resolve(entry, false)
	return err == nil
}
func (s *Server) communityCurrent(ctx context.Context, collection string) (*community.Program, error) {
	if !s.allowsCollection(collection) {
		return nil, fmt.Errorf("collection unavailable")
	}
	schedule, err := s.DB.Schedule(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	within, err := s.DB.ScheduleWithinScope(ctx, schedule, domain.CollectionScope{collection})
	if err != nil || !within {
		return nil, err
	}
	now := time.Now()
	if s.Clock != nil {
		now = s.Clock()
	}
	entries, err := s.DB.CurrentEntries(ctx, schedule, now)
	if err != nil {
		return nil, err
	}
	state := schedule.At(now, entries)
	if state.State != "playing" || state.Program == nil || state.Episode == nil || state.Episode.CollectionID != collection {
		return nil, nil
	}
	return &community.Program{Revision: state.Revision, Start: state.Program.Start, Episode: state.Program.EpisodeID, OriginalDate: state.Episode.Date}, nil
}

// communityFeatures tells clients whether accounts and chat run here, so they
// need not probe routes that answer 404 when the community module is off.
func (s *Server) communityFeatures() map[string]bool {
	c := s.Community
	return map[string]bool{"personal": c != nil, "chat": c != nil && (c.ChatRead || c.AudioChatRead)}
}
