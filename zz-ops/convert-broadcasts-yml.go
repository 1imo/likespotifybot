package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"likespotifybot/utils"
	"likespotifybot/utils/db"
)

// moveBroadcastToComplete relocates a successfully loaded YAML into assets/broadcasts/complete/.
func moveBroadcastToComplete(srcPath string) error {
	dir := filepath.Dir(srcPath)
	base := filepath.Base(srcPath)
	destDir := filepath.Join(dir, "complete")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}
	dest := filepath.Join(destDir, base)
	if err := os.Rename(srcPath, dest); err != nil {
		return err
	}
	fmt.Printf("moved %s -> complete/%s\n", base, base)
	return nil
}

type frontMatter struct {
	ID          string `yaml:"id"`
	Type        string `yaml:"type"`
	Audience    string `yaml:"audience"`
	ScheduledAt string `yaml:"scheduled_at"`
	// Order: optional load order when multiple YAMLs run in one convert: lower numbers first; omitted sorts last (stable by filename among ties / missing).
	Order *int `yaml:"order"`
	// Frequency: optional; stored on broadcasts row (not used by current sender — campaigns are queued when you run this tool).
	Frequency *int `yaml:"frequency"`
	// SeedOutgoing: when false, only upserts broadcasts; no broadcast_outgoing rows (definitions / templates only).
	SeedOutgoing *bool `yaml:"seed_outgoing"`
	// SkipOutgoingIfAlreadySeeded: when true, skip INSERT into broadcast_outgoing if this broadcast_id already has
	// any row (campaign already queued once). Set false (default) to keep adding new users via ON CONFLICT DO NOTHING.
	SkipOutgoingIfAlreadySeeded *bool `yaml:"skip_outgoing_if_already_seeded"`
	// OutgoingOnDuplicatePayload: when another broadcast already has the same JSON payload, we normally skip seeding
	// this id to avoid duplicate Telegram sends. Set true to seed anyway (e.g. separate campaign id, same text).
	OutgoingOnDuplicatePayload *bool `yaml:"outgoing_on_duplicate_payload"`
}

func (m *frontMatter) seedOutgoing() bool {
	if m == nil || m.SeedOutgoing == nil {
		return true
	}
	return *m.SeedOutgoing
}

func (m *frontMatter) skipIfAlreadySeeded() bool {
	if m == nil || m.SkipOutgoingIfAlreadySeeded == nil {
		return false
	}
	return *m.SkipOutgoingIfAlreadySeeded
}

func (m *frontMatter) outgoingOnDuplicatePayload() bool {
	if m == nil || m.OutgoingOnDuplicatePayload == nil {
		return false
	}
	return *m.OutgoingOnDuplicatePayload
}

func main() {
	utils.Env.InitOps()

	broadcastDir := filepath.Join(".", "assets", "broadcasts")
	paths, err := collectBroadcastYAML(broadcastDir)
	if err != nil {
		panic(err)
	}
	paths, err = sortBroadcastPathsByOrder(paths)
	if err != nil {
		panic(err)
	}

	database, err := db.DatabaseManager.Init(context.Background())
	if err != nil {
		panic(fmt.Errorf("open db: %w", err))
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for _, path := range paths {
		if err := upsertBroadcastAndSeedOutgoing(ctx, database, path); err != nil {
			panic(err)
		}
	}

	fmt.Printf("loaded %d broadcasts\n", len(paths))
}

// collectBroadcastYAML lists only .yml/.yaml files directly in dir (not subfolders such as templates/).
func collectBroadcastYAML(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	return out, nil
}

// sortBroadcastPathsByOrder orders paths for processing: lower "order" first; files without order go last (filename tie-break).
func sortBroadcastPathsByOrder(paths []string) ([]string, error) {
	type item struct {
		path  string
		order *int
	}
	items := make([]item, 0, len(paths))
	for _, p := range paths {
		var meta frontMatter
		var body map[string]any
		if err := utils.ParseFrontMatterYAML(p, &meta, &body); err != nil {
			return nil, fmt.Errorf("read order from %s: %w", p, err)
		}
		items = append(items, item{path: p, order: meta.Order})
	}
	sort.SliceStable(items, func(i, j int) bool {
		ai, aj := items[i].order, items[j].order
		switch {
		case ai == nil && aj == nil:
			return items[i].path < items[j].path
		case ai == nil:
			return false
		case aj == nil:
			return true
		case *ai != *aj:
			return *ai < *aj
		default:
			return items[i].path < items[j].path
		}
	})
	out := make([]string, len(items))
	for i := range items {
		out[i] = items[i].path
	}
	return out, nil
}

// broadcastIDTimePattern matches ..._YYYYMMDD_HHMM_... or ..._YYYYMMDD_HHMM end (e.g. msg_20260402_1600_features).
var broadcastIDTimePattern = regexp.MustCompile(`_(\d{8})_(\d{4})(?:_|$)`)

// createdAtFromBroadcastID sets created_at from the id: embedded _YYYYMMDD_HHMM_ is parsed as UTC.
// If absent, uses a stable sentinel so re-converts do not fight over timestamps (see ON CONFLICT: created_at preserved).
func createdAtFromBroadcastID(id string) time.Time {
	m := broadcastIDTimePattern.FindStringSubmatch(id)
	if len(m) != 3 {
		return time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	y, err1 := strconv.Atoi(m[1][0:4])
	mo, err2 := strconv.Atoi(m[1][4:6])
	d, err3 := strconv.Atoi(m[1][6:8])
	hh, err4 := strconv.Atoi(m[2][0:2])
	mm, err5 := strconv.Atoi(m[2][2:4])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil {
		return time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	t := time.Date(y, time.Month(mo), d, hh, mm, 0, 0, time.UTC)
	if t.Year() != y || int(t.Month()) != mo || t.Day() != d {
		return time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return t
}

func upsertBroadcastAndSeedOutgoing(ctx context.Context, database *db.Database, path string) error {
	var meta frontMatter
	var payload map[string]any
	if err := utils.ParseFrontMatterYAML(path, &meta, &payload); err != nil {
		return err
	}

	createdAt := createdAtFromBroadcastID(meta.ID)

	var scheduledAt any
	if strings.TrimSpace(meta.ScheduledAt) != "" {
		t, err := time.Parse(time.RFC3339, meta.ScheduledAt)
		if err != nil {
			return fmt.Errorf("parse scheduled_at %s: %w", path, err)
		}
		scheduledAt = t
	}

	tx, err := database.SQL().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx for %q: %w", meta.ID, err)
	}
	defer tx.Rollback()

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload %s: %w", path, err)
	}

	var freq sql.NullInt64
	if meta.Frequency != nil {
		freq = sql.NullInt64{Int64: int64(*meta.Frequency), Valid: true}
	}

	var audience sql.NullString
	if s := strings.TrimSpace(meta.Audience); s != "" {
		audience = sql.NullString{String: s, Valid: true}
	}

	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO broadcasts (id, type, payload, created_at, frequency, audience)
		 VALUES ($1, $2, $3::jsonb, $4, $5, $6)
		 ON CONFLICT (id)
		 DO UPDATE SET
		   type = EXCLUDED.type,
		   payload = EXCLUDED.payload,
		   frequency = EXCLUDED.frequency,
		   audience = EXCLUDED.audience`,
		meta.ID, meta.Type, string(payloadJSON), createdAt, freq, audience,
	)
	if err != nil {
		return fmt.Errorf("upsert broadcast %q: %w", meta.ID, err)
	}

	if !meta.seedOutgoing() {
		fmt.Printf("skip broadcast_outgoing for %s: seed_outgoing=false\n", meta.ID)
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit tx for %q: %w", meta.ID, err)
		}
		return moveBroadcastToComplete(path)
	}

	var duplicateBody bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM broadcasts
			WHERE payload = $1::jsonb AND id <> $2
		)`,
		string(payloadJSON), meta.ID,
	).Scan(&duplicateBody); err != nil {
		return fmt.Errorf("check duplicate broadcast body %q: %w", meta.ID, err)
	}

	if duplicateBody && !meta.outgoingOnDuplicatePayload() {
		fmt.Printf("skip broadcast_outgoing for %s: same payload already exists on another broadcast id (set outgoing_on_duplicate_payload: true to seed anyway)\n", meta.ID)
	} else {
		if meta.skipIfAlreadySeeded() {
			var n int
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(*)::int FROM broadcast_outgoing WHERE broadcast_id = $1`,
				meta.ID,
			).Scan(&n); err != nil {
				return fmt.Errorf("count broadcast_outgoing %q: %w", meta.ID, err)
			}
			if n > 0 {
				fmt.Printf("skip broadcast_outgoing for %s: skip_outgoing_if_already_seeded=true and %d row(s) already exist\n", meta.ID, n)
				if err := tx.Commit(); err != nil {
					return fmt.Errorf("commit tx for %q: %w", meta.ID, err)
				}
				return moveBroadcastToComplete(path)
			}
		}

		userFilter := "TRUE"
		switch strings.ToLower(strings.TrimSpace(meta.Audience)) {
		case "active_users":
			userFilter = "total_requests > 0"
		case "all":
			userFilter = "TRUE"
		}

		// Users (filtered by audience) UNION all known group/supergroup/channel chats from group_chats.
		seedQuery := fmt.Sprintf(
			`INSERT INTO broadcast_outgoing (broadcast_id, user_id, scheduled_at, sent_time)
			 SELECT $1, t.telegram_id, $2, NULL
			 FROM (
			   SELECT u.telegram_id FROM users u WHERE %s
			   UNION
			   SELECT g.telegram_id FROM group_chats g
			 ) AS t
			 ON CONFLICT (broadcast_id, user_id) DO NOTHING`,
			userFilter,
		)

		res, err := tx.ExecContext(ctx, seedQuery, meta.ID, scheduledAt)
		if err != nil {
			return fmt.Errorf("seed outgoing for %q: %w", meta.ID, err)
		}
		if rows, err := res.RowsAffected(); err == nil {
			fmt.Printf("seed %s: broadcast_outgoing rows inserted (new pairs) ~%d\n", meta.ID, rows)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx for %q: %w", meta.ID, err)
	}

	return moveBroadcastToComplete(path)
}
