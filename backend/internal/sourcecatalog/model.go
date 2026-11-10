package sourcecatalog

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

var ItemID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$`)
var Year = regexp.MustCompile(`^(19|20)[0-9]{2}$`)

type Snapshot struct {
	Metadata map[string]any   `json:"metadata"`
	Files    []map[string]any `json:"files"`
}

type Normalization struct {
	Version  string            `json:"version"`
	Warnings []string          `json:"warnings"`
	Derived  map[string]string `json:"derived"`
}

// Preserve the supplied raw bytes separately. Normalization makes no claims
// about concert identity or set boundaries. Source type is projected from the
// effective fields by the database, so corrections and re-imports share it.
func Normalize(collection, id string, raw []byte) (map[string]string, Normalization, error) {
	n := Normalization{Version: "live-music-v1", Warnings: []string{}, Derived: map[string]string{}}
	var snapshot Snapshot
	if !ItemID.MatchString(id) || len(raw) > 32<<20 || json.Unmarshal(raw, &snapshot) != nil || snapshot.Metadata == nil || snapshot.Files == nil {
		return nil, n, fmt.Errorf("invalid or oversized metadata snapshot")
	}
	if identity, ok := snapshot.Metadata["identifier"].(string); !ok || identity != id {
		return nil, n, fmt.Errorf("source identity mismatch")
	}
	inCollection := false
	for _, c := range Strings(snapshot.Metadata["collection"]) {
		if c == collection {
			inCollection = true
		}
	}
	if !inCollection || Text(snapshot.Metadata["mediatype"]) == "collection" {
		return nil, n, fmt.Errorf("source is outside the selected recording collection")
	}
	result := map[string]string{}
	for _, key := range []string{"title", "creator", "date", "venue", "coverage", "description", "taper", "transferer", "source", "lineage", "subject"} {
		value := Text(snapshot.Metadata[key])
		result[key] = value
		if key == "description" {
			result[key] = PlainText(value)
		}
	}
	date := result["date"]
	if len(date) >= 10 {
		if _, err := time.Parse("2006-01-02", date[:10]); err == nil && (len(date) == 10 || (len(date) > 10 && date[10] == 'T')) {
			result["date"] = date[:10]
			if date != result["date"] {
				n.Derived["date"] = "Calendar date from source timestamp"
			}
		} else {
			n.Warnings = append(n.Warnings, "Source date is incomplete or invalid; no complete date inferred")
		}
	} else if date != "" {
		n.Warnings = append(n.Warnings, "Source date is incomplete; retained as supplied")
	}
	if result["venue"] == "" {
		title := regexp.MustCompile(`(?i) Live at (.+) on [0-9]{4}-[0-9]{2}-[0-9]{2}$`).FindStringSubmatch(result["title"])
		if len(title) == 2 {
			result["venue"] = strings.TrimSpace(title[1])
			n.Derived["venue"] = "Extracted from source title; not independently verified"
		}
	}
	if result["title"] == "" {
		result["title"] = id
		n.Derived["title"] = "Source identifier used because title is missing"
	}
	songs := []string{}
	for _, file := range snapshot.Files {
		if file == nil {
			return nil, n, fmt.Errorf("file inventory contains a non-object entry")
		}
		if title := Text(file["title"]); title != "" {
			songs = append(songs, title)
		}
	}
	result["songs"] = strings.Join(songs, " ")
	return result, n, nil
}
func Strings(v any) []string {
	switch v := v.(type) {
	case string:
		return []string{v}
	case []any:
		out := []string{}
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
func Text(v any) string { return strings.Join(strings.Fields(strings.Join(Strings(v), ", ")), " ") }
func PlainText(value string) string {
	z := html.NewTokenizer(strings.NewReader(value))
	out := strings.Builder{}
	hidden := 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			return strings.Join(strings.Fields(out.String()), " ")
		case html.StartTagToken:
			token := z.Token()
			if token.Data == "script" || token.Data == "style" {
				hidden++
			}
			out.WriteByte(' ')
		case html.EndTagToken:
			token := z.Token()
			if (token.Data == "script" || token.Data == "style") && hidden > 0 {
				hidden--
			}
			out.WriteByte(' ')
		case html.SelfClosingTagToken:
			out.WriteByte(' ')
		case html.TextToken:
			if hidden == 0 {
				out.Write(z.Text())
			}
		}
	}
}

// SourceTypes are the recording source classifications a filter can select.
var SourceTypes = map[string]bool{"soundboard": true, "audience": true, "matrix": true, "fm_radio": true, "other": true, "unknown": true}

var editable = map[string]bool{"source_type": true, "title": true, "creator": true, "date": true, "venue": true, "coverage": true, "description": true, "source": true, "lineage": true, "taper": true, "transferer": true, "subject": true}

func ValidatePatch(patch map[string]any, notes string) error {
	if patch == nil || len(patch) == 0 || len(patch) > len(editable) || len(strings.TrimSpace(notes)) == 0 || len(notes) > 4000 {
		return fmt.Errorf("a bounded metadata patch and review notes are required")
	}
	for key, value := range patch {
		if !editable[key] {
			return fmt.Errorf("field %q cannot be edited", key)
		}
		if value == nil {
			continue
		} // null removes an override and restores the source projection.
		s, ok := value.(string)
		if !ok || len(s) > 16000 {
			return fmt.Errorf("field %q requires bounded text or null", key)
		}
		if key == "source_type" && !SourceTypes[s] {
			return fmt.Errorf("use a known source type")
		}
		if key == "title" && strings.TrimSpace(s) == "" {
			return fmt.Errorf("title must not be blank")
		}
		if key == "date" && s != "" {
			if _, err := time.Parse("2006-01-02", s); err != nil {
				return fmt.Errorf("use a complete YYYY-MM-DD date")
			}
		}
	}
	return nil
}
