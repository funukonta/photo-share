package storage

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/photo-share/backend/config"
	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a requested photo does not exist.
var ErrNotFound = errors.New("photo not found")

// ErrEventNotFound is returned when a room code does not match any event.
var ErrEventNotFound = errors.New("event not found")

// Photo represents a single uploaded graduation memory.
type Photo struct {
	ID          string    `json:"id"`
	EventID     string    `json:"eventId"`
	ObjectKey   string    `json:"-"`
	ContentType string    `json:"contentType"`
	Nickname    string    `json:"nickname"`
	Caption     string    `json:"caption"`
	DeviceID    string    `json:"deviceId,omitempty"`
	IPAddress   string    `json:"-"`
	Hidden      bool      `json:"hidden,omitempty"`
	ImageURL    string    `json:"imageUrl"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Event is one photo wall. Its ID is the 6-character room code used in URLs (/k7p2qx, /k7p2qx/wall).
type Event struct {
	ID                 string    `json:"id"`
	Title              string    `json:"title"`
	Subtitle           string    `json:"subtitle"`
	EventCode          string    `json:"eventCode"`
	MaxPhotosPerPerson int       `json:"maxPhotosPerPerson"`
	MaxPhotosPerIP     int       `json:"maxPhotosPerIp"`
	UploadsOpen        bool      `json:"uploadsOpen"`
	CreatedAt          time.Time `json:"createdAt"`
	// Filled by ListEvents/GetEvent.
	PhotoCount  int `json:"photoCount"`
	HiddenCount int `json:"hiddenCount"`
}

// EventSettings holds the legacy single-event settings (pre multi-event). Only read during migration.
type EventSettings struct {
	EventTitle         string
	EventSubtitle      string
	EventCode          string
	MaxPhotosPerPerson int
	MaxPhotosPerIP     int
}

// Room codes avoid look-alike characters (0/o, 1/l/i) so they can be read off a projector and typed.
const roomAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// RoomCodeLength is the length of generated room codes.
const RoomCodeLength = 6

var roomCodePattern = regexp.MustCompile(`^[a-z0-9]{6}$`)

// Paths a room code must never shadow in the SPA router.
var reservedRoomCodes = map[string]bool{"assets": true, "health": true}

// NormalizeRoomCode lowercases and trims a code; ok is false when it can't be a room code.
func NormalizeRoomCode(raw string) (string, bool) {
	code := strings.ToLower(strings.TrimSpace(raw))
	return code, roomCodePattern.MatchString(code)
}

func newRoomCode() (string, error) {
	b := make([]byte, RoomCodeLength)
	max := big.NewInt(int64(len(roomAlphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = roomAlphabet[n.Int64()]
	}
	return string(b), nil
}

// SQLiteStore manages events, photo metadata, and device quotas.
type SQLiteStore struct {
	db  *sql.DB
	cfg *config.Config
}

// NewSQLiteStore opens/creates the SQLite database file, runs schema migrations, and moves pre-multi-event
// photos into a generated event.
func NewSQLiteStore(cfg *config.Config) (*SQLiteStore, error) {
	if cfg.SQLitePath != ":memory:" && !strings.HasPrefix(cfg.SQLitePath, "file:") {
		dir := filepath.Dir(cfg.SQLitePath)
		if dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create sqlite directory %q: %w", dir, err)
			}
		}
	}

	db, err := sql.Open("sqlite", cfg.SQLitePath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure sqlite pragmas: %w", err)
	}

	store := &SQLiteStore{db: db, cfg: cfg}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// Close closes the underlying SQLite connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS photos (
		id TEXT PRIMARY KEY,
		object_key TEXT NOT NULL,
		content_type TEXT NOT NULL DEFAULT 'image/jpeg',
		nickname TEXT NOT NULL,
		caption TEXT NOT NULL DEFAULT '',
		device_id TEXT NOT NULL,
		ip_address TEXT NOT NULL DEFAULT '',
		hidden INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS events (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		subtitle TEXT NOT NULL DEFAULT '',
		event_code TEXT NOT NULL DEFAULT '',
		max_photos_per_person INTEGER NOT NULL DEFAULT 5,
		max_photos_per_ip INTEGER NOT NULL DEFAULT 150,
		uploads_open INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL
	);
	`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrate sqlite schema: %w", err)
	}

	hasEventID, err := s.columnExists("photos", "event_id")
	if err != nil {
		return err
	}
	if !hasEventID {
		if _, err := s.db.Exec(`ALTER TABLE photos ADD COLUMN event_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add photos.event_id: %w", err)
		}
	}

	indexes := `
	CREATE INDEX IF NOT EXISTS idx_photos_event_created ON photos(event_id, created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_photos_event_device ON photos(event_id, device_id);
	CREATE INDEX IF NOT EXISTS idx_photos_event_ip ON photos(event_id, ip_address);
	`
	if _, err := s.db.Exec(indexes); err != nil {
		return fmt.Errorf("create photo indexes: %w", err)
	}

	return s.adoptLegacyPhotos()
}

func (s *SQLiteStore) columnExists(table, column string) (bool, error) {
	rows, err := s.db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid       int
			name, typ string
			notNull   int
			dflt      sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// adoptLegacyPhotos moves photos uploaded before events existed into one generated event,
// built from the old single-event settings, so nothing from an earlier run is lost.
func (s *SQLiteStore) adoptLegacyPhotos() error {
	var orphans int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM photos WHERE event_id = ''`).Scan(&orphans); err != nil {
		return fmt.Errorf("count legacy photos: %w", err)
	}
	if orphans == 0 {
		return nil
	}

	legacy, err := s.legacySettings()
	if err != nil {
		return err
	}
	ev, err := s.CreateEvent(Event{
		Title:              legacy.EventTitle,
		Subtitle:           legacy.EventSubtitle,
		EventCode:          legacy.EventCode,
		MaxPhotosPerPerson: legacy.MaxPhotosPerPerson,
		MaxPhotosPerIP:     legacy.MaxPhotosPerIP,
		UploadsOpen:        true,
	})
	if err != nil {
		return fmt.Errorf("create event for legacy photos: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE photos SET event_id = ? WHERE event_id = ''`, ev.ID); err != nil {
		return fmt.Errorf("move legacy photos: %w", err)
	}
	log.Printf("moved %d photos from the single-event version into event %q (room %s)", orphans, ev.Title, ev.ID)
	return nil
}

func (s *SQLiteStore) legacySettings() (EventSettings, error) {
	settings := EventSettings{
		EventTitle:         s.cfg.EventTitle,
		EventSubtitle:      s.cfg.EventSubtitle,
		EventCode:          s.cfg.EventCode,
		MaxPhotosPerPerson: s.cfg.MaxPhotosPerPerson,
		MaxPhotosPerIP:     s.cfg.MaxPhotosPerIP,
	}
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return settings, fmt.Errorf("read legacy settings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return settings, err
		}
		switch k {
		case "event_title":
			settings.EventTitle = v
		case "event_subtitle":
			settings.EventSubtitle = v
		case "event_code":
			settings.EventCode = v
		case "max_photos_per_person":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				settings.MaxPhotosPerPerson = n
			}
		case "max_photos_per_ip":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				settings.MaxPhotosPerIP = n
			}
		}
	}
	if strings.TrimSpace(settings.EventTitle) == "" {
		settings.EventTitle = "Photo wall"
	}
	return settings, rows.Err()
}

// ---------------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------------

const eventColumns = `e.id, e.title, e.subtitle, e.event_code, e.max_photos_per_person, e.max_photos_per_ip, e.uploads_open, e.created_at,
	COALESCE(SUM(CASE WHEN p.id IS NOT NULL AND p.hidden = 0 THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN p.hidden = 1 THEN 1 ELSE 0 END), 0)`

func scanEvent(scanner rowScanner) (Event, error) {
	var (
		e       Event
		open    int
		created string
	)
	if err := scanner.Scan(&e.ID, &e.Title, &e.Subtitle, &e.EventCode, &e.MaxPhotosPerPerson, &e.MaxPhotosPerIP, &open, &created, &e.PhotoCount, &e.HiddenCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Event{}, ErrEventNotFound
		}
		return Event{}, err
	}
	e.UploadsOpen = open != 0
	e.CreatedAt = parseTime(created)
	return e, nil
}

// CreateEvent stores a new event under a freshly generated, unused room code.
func (s *SQLiteStore) CreateEvent(e Event) (Event, error) {
	if e.MaxPhotosPerPerson <= 0 {
		e.MaxPhotosPerPerson = s.cfg.MaxPhotosPerPerson
	}
	if e.MaxPhotosPerPerson <= 0 {
		e.MaxPhotosPerPerson = 5
	}
	if e.MaxPhotosPerIP <= 0 {
		e.MaxPhotosPerIP = s.cfg.MaxPhotosPerIP
	}
	if e.MaxPhotosPerIP <= 0 {
		e.MaxPhotosPerIP = 150
	}
	e.CreatedAt = time.Now().UTC()

	for attempt := 0; attempt < 10; attempt++ {
		code, err := newRoomCode()
		if err != nil {
			return Event{}, fmt.Errorf("generate room code: %w", err)
		}
		if reservedRoomCodes[code] {
			continue
		}
		e.ID = code
		_, err = s.db.Exec(
			`INSERT INTO events (id, title, subtitle, event_code, max_photos_per_person, max_photos_per_ip, uploads_open, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			e.ID, strings.TrimSpace(e.Title), strings.TrimSpace(e.Subtitle), strings.TrimSpace(e.EventCode),
			e.MaxPhotosPerPerson, e.MaxPhotosPerIP, boolInt(e.UploadsOpen), e.CreatedAt.Format(time.RFC3339Nano),
		)
		if err == nil {
			return s.GetEvent(e.ID)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Event{}, fmt.Errorf("insert event: %w", err)
		}
	}
	return Event{}, errors.New("could not find a free room code")
}

// GetEvent loads one event (with photo counts) by room code.
func (s *SQLiteStore) GetEvent(id string) (Event, error) {
	row := s.db.QueryRow(
		`SELECT `+eventColumns+` FROM events e LEFT JOIN photos p ON p.event_id = e.id WHERE e.id = ? GROUP BY e.id`, id,
	)
	return scanEvent(row)
}

// ListEvents returns every event, newest first, with photo counts.
func (s *SQLiteStore) ListEvents() ([]Event, error) {
	rows, err := s.db.Query(
		`SELECT ` + eventColumns + ` FROM events e LEFT JOIN photos p ON p.event_id = e.id GROUP BY e.id ORDER BY e.created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	events := make([]Event, 0)
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// UpdateEvent saves the editable fields of an event.
func (s *SQLiteStore) UpdateEvent(e Event) (Event, error) {
	res, err := s.db.Exec(
		`UPDATE events SET title = ?, subtitle = ?, event_code = ?, max_photos_per_person = ?, max_photos_per_ip = ?, uploads_open = ?
		 WHERE id = ?`,
		strings.TrimSpace(e.Title), strings.TrimSpace(e.Subtitle), strings.TrimSpace(e.EventCode),
		e.MaxPhotosPerPerson, e.MaxPhotosPerIP, boolInt(e.UploadsOpen), e.ID,
	)
	if err != nil {
		return Event{}, fmt.Errorf("update event: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Event{}, ErrEventNotFound
	}
	return s.GetEvent(e.ID)
}

// DeleteEvent removes the event and its photo records, returning the photos so the caller can delete the files.
func (s *SQLiteStore) DeleteEvent(id string) ([]Photo, error) {
	photos, err := s.ListAllPhotos(id)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM photos WHERE event_id = ?`, id); err != nil {
		return nil, fmt.Errorf("delete event photos: %w", err)
	}
	res, err := tx.Exec(`DELETE FROM events WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("delete event: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrEventNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return photos, nil
}

// ---------------------------------------------------------------------------
// Photos
// ---------------------------------------------------------------------------

const photoColumns = `id, event_id, object_key, content_type, nickname, caption, device_id, ip_address, hidden, created_at`

// InsertPhoto saves a new photo record in SQLite. p.EventID must be set.
func (s *SQLiteStore) InsertPhoto(p Photo) (Photo, error) {
	if p.EventID == "" {
		return Photo{}, errors.New("photo has no event")
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	p.ImageURL = fmt.Sprintf("/api/photos/%s/image", p.ID)

	_, err := s.db.Exec(
		`INSERT INTO photos (`+photoColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.EventID, p.ObjectKey, p.ContentType, p.Nickname, p.Caption, p.DeviceID, p.IPAddress, boolInt(p.Hidden),
		p.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return Photo{}, fmt.Errorf("insert photo: %w", err)
	}
	return p, nil
}

// GetPhotoByID retrieves a single photo record by ID.
func (s *SQLiteStore) GetPhotoByID(id string) (Photo, error) {
	return scanPhotoRow(s.db.QueryRow(`SELECT `+photoColumns+` FROM photos WHERE id = ?`, id))
}

func (s *SQLiteStore) queryPhotos(query string, args ...any) ([]Photo, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	photos := make([]Photo, 0)
	for rows.Next() {
		p, err := scanPhotoRow(rows)
		if err != nil {
			return nil, err
		}
		photos = append(photos, p)
	}
	return photos, rows.Err()
}

// ListVisiblePhotos returns an event's non-hidden photos, newest first, without device IDs.
func (s *SQLiteStore) ListVisiblePhotos(eventID string) ([]Photo, error) {
	photos, err := s.queryPhotos(
		`SELECT `+photoColumns+` FROM photos WHERE event_id = ? AND hidden = 0 ORDER BY created_at DESC`, eventID,
	)
	if err != nil {
		return nil, fmt.Errorf("list visible photos: %w", err)
	}
	for i := range photos {
		photos[i].DeviceID = "" // public listing
	}
	return photos, nil
}

// ListAllPhotos returns all of an event's photos (including hidden ones and device IDs) for moderation and export.
func (s *SQLiteStore) ListAllPhotos(eventID string) ([]Photo, error) {
	photos, err := s.queryPhotos(
		`SELECT `+photoColumns+` FROM photos WHERE event_id = ? ORDER BY created_at DESC`, eventID,
	)
	if err != nil {
		return nil, fmt.Errorf("list all photos: %w", err)
	}
	return photos, nil
}

// ListPhotosByDevice returns one device's visible photos in an event, newest first.
func (s *SQLiteStore) ListPhotosByDevice(eventID, deviceID string) ([]Photo, error) {
	photos, err := s.queryPhotos(
		`SELECT `+photoColumns+` FROM photos WHERE event_id = ? AND device_id = ? AND hidden = 0 ORDER BY created_at DESC`,
		eventID, deviceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list photos by device: %w", err)
	}
	return photos, nil
}

// CountByDeviceID returns how many visible photos a device has in an event (its quota usage).
func (s *SQLiteStore) CountByDeviceID(eventID, deviceID string) (int, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM photos WHERE event_id = ? AND device_id = ? AND hidden = 0`, eventID, deviceID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count photos by device: %w", err)
	}
	return count, nil
}

// CountByIPAddress returns how many visible photos came from one network address in an event.
func (s *SQLiteStore) CountByIPAddress(eventID, ip string) (int, error) {
	if ip == "" {
		return 0, nil
	}
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM photos WHERE event_id = ? AND ip_address = ? AND hidden = 0`, eventID, ip,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count photos by ip: %w", err)
	}
	return count, nil
}

// SetPhotoHidden toggles the hidden flag for a photo.
func (s *SQLiteStore) SetPhotoHidden(id string, hidden bool) (Photo, error) {
	res, err := s.db.Exec(`UPDATE photos SET hidden = ? WHERE id = ?`, boolInt(hidden), id)
	if err != nil {
		return Photo{}, fmt.Errorf("update photo hidden: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Photo{}, ErrNotFound
	}
	return s.GetPhotoByID(id)
}

// DeletePhoto removes a photo record and returns its metadata so the caller can also remove the stored file.
func (s *SQLiteStore) DeletePhoto(id string) (Photo, error) {
	p, err := s.GetPhotoByID(id)
	if err != nil {
		return Photo{}, err
	}
	if _, err := s.db.Exec(`DELETE FROM photos WHERE id = ?`, id); err != nil {
		return Photo{}, fmt.Errorf("delete photo record: %w", err)
	}
	return p, nil
}

// ResetDeviceQuota detaches a device's photos in one event from its device ID, so its quota there returns to 0
// while the photos stay on the wall. Other events are untouched.
func (s *SQLiteStore) ResetDeviceQuota(eventID, deviceID string) (int64, error) {
	archived := fmt.Sprintf("reset:%s:%d", deviceID, time.Now().Unix())
	res, err := s.db.Exec(`UPDATE photos SET device_id = ? WHERE event_id = ? AND device_id = ?`, archived, eventID, deviceID)
	if err != nil {
		return 0, fmt.Errorf("reset device quota: %w", err)
	}
	return res.RowsAffected()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanPhotoRow(scanner rowScanner) (Photo, error) {
	var (
		p         Photo
		hiddenInt int
		created   string
	)
	err := scanner.Scan(&p.ID, &p.EventID, &p.ObjectKey, &p.ContentType, &p.Nickname, &p.Caption, &p.DeviceID, &p.IPAddress, &hiddenInt, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Photo{}, ErrNotFound
		}
		return Photo{}, err
	}
	p.Hidden = hiddenInt != 0
	p.ImageURL = fmt.Sprintf("/api/photos/%s/image", p.ID)
	p.CreatedAt = parseTime(created)
	return p, nil
}

func parseTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
