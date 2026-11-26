package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/photo-share/backend/config"
	"github.com/photo-share/backend/handlers"
	"github.com/photo-share/backend/storage"
	_ "modernc.org/sqlite"
)

// setupTestServer starts the API with an in-memory object store and one event; it returns the event's room code.
func setupTestServer(t *testing.T, modifyCfg func(*config.Config)) (*httptest.Server, *handlers.API, *storage.SQLiteStore, string) {
	t.Helper()

	cfg := &config.Config{
		Port:               "0",
		SQLitePath:         filepath.Join(t.TempDir(), "test.db"),
		DeviceSecret:       "test-hmac-secret-2026",
		HostPIN:            "2026",
		EventTitle:         "Batch 42 Graduation Wall 🎓",
		EventSubtitle:      "Drop up to 5 photos from our journey!",
		MaxPhotosPerPerson: 5,
		MaxPhotosPerIP:     150,
	}
	if modifyCfg != nil {
		modifyCfg(cfg)
	}

	db, err := storage.NewSQLiteStore(cfg)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ev, err := db.CreateEvent(storage.Event{
		Title:              cfg.EventTitle,
		Subtitle:           cfg.EventSubtitle,
		EventCode:          cfg.EventCode,
		MaxPhotosPerPerson: cfg.MaxPhotosPerPerson,
		MaxPhotosPerIP:     cfg.MaxPhotosPerIP,
		UploadsOpen:        true,
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}

	api := handlers.NewAPI(cfg, db, storage.NewMemoryObjectStore(), handlers.NewSSEHub())
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	return ts, api, db, ev.ID
}

func makeSampleJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: 240, G: 180, B: 60, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatalf("failed to encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func roomURL(base, room, path string) string {
	return fmt.Sprintf("%s/api/rooms/%s%s", base, room, path)
}

func uploadMultipartPhoto(t *testing.T, base, room, deviceToken, clientIP, nickname, caption, eventCode string, imgBytes []byte) (*http.Response, []byte) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	_ = writer.WriteField("nickname", nickname)
	_ = writer.WriteField("caption", caption)
	if eventCode != "" {
		_ = writer.WriteField("event_code", eventCode)
	}

	part, err := writer.CreateFormFile("photo", "graduation.jpg")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(imgBytes); err != nil {
		t.Fatalf("write image bytes: %v", err)
	}
	_ = writer.Close()

	req, err := http.NewRequest(http.MethodPost, roomURL(base, room, "/photos"), &body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if deviceToken != "" {
		req.Header.Set(handlers.DeviceHeaderName, deviceToken)
	}
	if clientIP != "" {
		req.Header.Set("X-Forwarded-For", clientIP)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST photos: %v", err)
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(resp.Body)
	return resp, respBytes
}

func TestConfigAndMeIdentityFlow(t *testing.T) {
	ts, _, _, room := setupTestServer(t, nil)

	res, err := http.Get(roomURL(ts.URL, room, "/config"))
	if err != nil {
		t.Fatalf("GET config failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	var cfgResp handlers.PublicConfigResponse
	if err := json.NewDecoder(res.Body).Decode(&cfgResp); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfgResp.Title != "Batch 42 Graduation Wall 🎓" || cfgResp.MaxPhotosPerPerson != 5 || cfgResp.Room != room || !cfgResp.UploadsOpen {
		t.Fatalf("unexpected config response: %+v", cfgResp)
	}

	meRes, err := http.Get(roomURL(ts.URL, room, "/me"))
	if err != nil {
		t.Fatalf("GET me failed: %v", err)
	}
	defer meRes.Body.Close()

	var me handlers.MeResponse
	if err := json.NewDecoder(meRes.Body).Decode(&me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.DeviceID == "" || me.DeviceToken == "" {
		t.Fatalf("expected deviceId and deviceToken to be issued, got %+v", me)
	}
	if me.RemainingCount != 5 || me.UploadedCount != 0 {
		t.Fatalf("expected 5 remaining and 0 uploaded, got %+v", me)
	}
}

func TestUnknownRoomIs404(t *testing.T) {
	ts, _, _, _ := setupTestServer(t, nil)
	for _, path := range []string{"/api/rooms/zzzzzz/config", "/api/rooms/zzzzzz/photos", "/api/rooms/not-a-code/config", "/api/rooms/ABC/me"} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound || !strings.Contains(string(body), "ROOM_NOT_FOUND") {
			t.Fatalf("%s: expected 404 ROOM_NOT_FOUND, got %d %s", path, res.StatusCode, body)
		}
	}
}

func TestRoomCodeIsCaseInsensitive(t *testing.T) {
	ts, _, _, room := setupTestServer(t, nil)
	res, err := http.Get(roomURL(ts.URL, strings.ToUpper(room), "/config"))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected uppercase room code to work, got %d", res.StatusCode)
	}
}

func TestFivePhotoPerPersonQuotaAndSharedWiFiIsolation(t *testing.T) {
	ts, api, _, room := setupTestServer(t, nil)
	imgBytes := makeSampleJPEG(t)

	deviceAToken := api.Identity.SignDeviceID("device-alice-1111")
	deviceBToken := api.Identity.SignDeviceID("device-bob-2222")
	sharedVenueIP := "203.0.113.50"

	sseCh, unsubscribe := api.Hub.Subscribe(room)
	defer unsubscribe()

	var lastPhotoID string
	for i := 1; i <= 5; i++ {
		resp, body := uploadMultipartPhoto(t, ts.URL, room, deviceAToken, sharedVenueIP, "Alice", fmt.Sprintf("Memory #%d", i), "", imgBytes)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("Alice upload #%d expected 201, got %d: %s", i, resp.StatusCode, string(body))
		}
		var payload struct {
			Photo          storage.Photo `json:"photo"`
			UploadedCount  int           `json:"uploadedCount"`
			RemainingCount int           `json:"remainingCount"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("unmarshal upload response: %v", err)
		}
		if payload.UploadedCount != i || payload.RemainingCount != (5-i) {
			t.Fatalf("upload #%d quota mismatch: got uploaded=%d remaining=%d", i, payload.UploadedCount, payload.RemainingCount)
		}
		if payload.Photo.EventID != room {
			t.Fatalf("photo stored under %q, want %q", payload.Photo.EventID, room)
		}
		lastPhotoID = payload.Photo.ID
	}

	select {
	case msg := <-sseCh:
		if !strings.Contains(string(msg), "event: photo_added") {
			t.Fatalf("expected SSE photo_added event, got: %s", string(msg))
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for SSE photo_added broadcast")
	}

	resp6, body6 := uploadMultipartPhoto(t, ts.URL, room, deviceAToken, sharedVenueIP, "Alice", "over limit", "", imgBytes)
	if resp6.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("Alice upload #6 expected 429, got %d: %s", resp6.StatusCode, string(body6))
	}

	respBob, bodyBob := uploadMultipartPhoto(t, ts.URL, room, deviceBToken, sharedVenueIP, "Bob", "Bob on shared Wi-Fi", "", imgBytes)
	if respBob.StatusCode != http.StatusCreated {
		t.Fatalf("Bob upload on shared Wi-Fi expected 201, got %d: %s", respBob.StatusCode, string(bodyBob))
	}

	imgRes, err := http.Get(fmt.Sprintf("%s/api/photos/%s/image", ts.URL, lastPhotoID))
	if err != nil {
		t.Fatalf("GET photo image failed: %v", err)
	}
	defer imgRes.Body.Close()
	if imgRes.StatusCode != http.StatusOK || imgRes.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("expected 200 image/jpeg, got %d %q", imgRes.StatusCode, imgRes.Header.Get("Content-Type"))
	}
	streamedBytes, _ := io.ReadAll(imgRes.Body)
	if !bytes.Equal(streamedBytes, imgBytes) {
		t.Fatalf("streamed image bytes did not match uploaded JPEG")
	}
}

func TestEventCodeProtectionAndReactions(t *testing.T) {
	ts, api, _, room := setupTestServer(t, func(c *config.Config) {
		c.EventCode = "GRAD2026"
	})
	imgBytes := makeSampleJPEG(t)
	token := api.Identity.SignDeviceID("device-charlie-3333")

	respFail, _ := uploadMultipartPhoto(t, ts.URL, room, token, "10.0.0.1", "Charlie", "No code", "", imgBytes)
	if respFail.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 when event_code missing, got %d", respFail.StatusCode)
	}

	respOK, _ := uploadMultipartPhoto(t, ts.URL, room, token, "10.0.0.1", "Charlie", "With code", "grad2026", imgBytes)
	if respOK.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 when valid event_code supplied, got %d", respOK.StatusCode)
	}

	sseCh, unsubscribe := api.Hub.Subscribe(room)
	defer unsubscribe()

	rResp, err := http.Post(roomURL(ts.URL, room, "/reactions"), "application/json", strings.NewReader(`{"emoji":"🎉","nickname":"Charlie"}`))
	if err != nil {
		t.Fatalf("POST reactions failed: %v", err)
	}
	defer rResp.Body.Close()
	if rResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from reactions, got %d", rResp.StatusCode)
	}

	select {
	case frame := <-sseCh:
		if !strings.Contains(string(frame), "event: reaction") || !strings.Contains(string(frame), "🎉") {
			t.Fatalf("unexpected reaction SSE frame: %s", string(frame))
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for reaction SSE frame")
	}
}

func TestEventsAreIsolated(t *testing.T) {
	ts, api, db, roomA := setupTestServer(t, func(c *config.Config) { c.MaxPhotosPerPerson = 2 })
	evB, err := db.CreateEvent(storage.Event{Title: "Batch 43", MaxPhotosPerPerson: 2, UploadsOpen: true})
	if err != nil {
		t.Fatalf("CreateEvent B: %v", err)
	}
	roomB := evB.ID
	if roomA == roomB {
		t.Fatalf("room codes collided")
	}

	img := makeSampleJPEG(t)
	token := api.Identity.SignDeviceID("device-dewi-4444")

	sseA, unsubA := api.Hub.Subscribe(roomA)
	defer unsubA()
	sseB, unsubB := api.Hub.Subscribe(roomB)
	defer unsubB()

	// Same phone uses its full allowance in A, and still has a full allowance in B.
	for i := 0; i < 2; i++ {
		if resp, body := uploadMultipartPhoto(t, ts.URL, roomA, token, "", "Dewi", "A", "", img); resp.StatusCode != http.StatusCreated {
			t.Fatalf("upload A %d: %d %s", i, resp.StatusCode, body)
		}
	}
	if resp, _ := uploadMultipartPhoto(t, ts.URL, roomA, token, "", "Dewi", "A over", "", img); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("A 3rd upload expected 429, got %d", resp.StatusCode)
	}
	if resp, body := uploadMultipartPhoto(t, ts.URL, roomB, token, "", "Dewi", "B", "", img); resp.StatusCode != http.StatusCreated {
		t.Fatalf("quota must be per event; B upload got %d %s", resp.StatusCode, body)
	}

	// Each wall only sees its own photos.
	list := func(room string) []storage.Photo {
		res, err := http.Get(roomURL(ts.URL, room, "/photos"))
		if err != nil {
			t.Fatalf("list %s: %v", room, err)
		}
		defer res.Body.Close()
		var out struct {
			Photos []storage.Photo `json:"photos"`
		}
		_ = json.NewDecoder(res.Body).Decode(&out)
		return out.Photos
	}
	a, b := list(roomA), list(roomB)
	if len(a) != 2 || len(b) != 1 {
		t.Fatalf("expected 2 photos in A and 1 in B, got %d and %d", len(a), len(b))
	}
	for _, p := range a {
		if p.EventID != roomA {
			t.Fatalf("room A listed a photo from %s", p.EventID)
		}
	}

	// Live events don't leak between rooms: A's stream holds only A's 2 uploads.
	drain := func(ch chan []byte) int {
		n := 0
		for {
			select {
			case <-ch:
				n++
			case <-time.After(150 * time.Millisecond):
				return n
			}
		}
	}
	if n := drain(sseA); n != 2 {
		t.Fatalf("room A stream got %d events, want 2", n)
	}
	if n := drain(sseB); n != 1 {
		t.Fatalf("room B stream got %d events, want 1", n)
	}
}

func TestLegacyPhotosMoveIntoAnEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	cfg := &config.Config{SQLitePath: path, EventTitle: "Env title", MaxPhotosPerPerson: 5, MaxPhotosPerIP: 150}

	// Build a database the way the single-event version left it: photos without event_id, settings rows.
	db, err := storage.NewSQLiteStore(cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ev, _ := db.CreateEvent(storage.Event{Title: "tmp", UploadsOpen: true})
	if _, err := db.InsertPhoto(storage.Photo{ID: "p1", EventID: ev.ID, ObjectKey: "k1", ContentType: "image/jpeg", Nickname: "old", DeviceID: "d"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	_ = db.Close()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	for _, stmt := range []string{
		`UPDATE photos SET event_id = ''`,
		`DELETE FROM events`,
		`INSERT INTO settings (key, value) VALUES ('event_title', 'Old Graduation Wall')`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	_ = raw.Close()

	db, err = storage.NewSQLiteStore(cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db.Close()
	events, _ := db.ListEvents()
	if len(events) != 1 || events[0].Title != "Old Graduation Wall" || events[0].PhotoCount != 1 {
		t.Fatalf("expected one migrated event with the old title and 1 photo, got %+v", events)
	}
	p, _ := db.GetPhotoByID("p1")
	if p.EventID != events[0].ID {
		t.Fatalf("photo not moved into migrated event")
	}
}
