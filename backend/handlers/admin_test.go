package handlers_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/photo-share/backend/config"
	"github.com/photo-share/backend/handlers"
	"github.com/photo-share/backend/storage"
)

func adminRequest(t *testing.T, client *http.Client, method, url, pin string, body any) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if pin != "" {
		req.Header.Set(handlers.HostPinHeader, pin)
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

func uploadOne(t *testing.T, base, room string, api *handlers.API, deviceID, nickname, caption string) storage.Photo {
	t.Helper()
	resp, body := uploadMultipartPhoto(t, base, room, api.Identity.SignDeviceID(deviceID), "198.51.100.7", nickname, caption, "", makeSampleJPEG(t))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload expected 201, got %d: %s", resp.StatusCode, body)
	}
	var payload struct {
		Photo storage.Photo `json:"photo"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	return payload.Photo
}

func nextFrame(t *testing.T, ch chan []byte) string {
	t.Helper()
	select {
	case msg := <-ch:
		return string(msg)
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for SSE frame")
	}
	return ""
}

func drain(ch chan []byte) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func adminEventURL(base, room, path string) string {
	return base + "/api/admin/events/" + room + path
}

func TestAdminLoginSessionAndLockout(t *testing.T) {
	ts, _, _, _ := setupTestServer(t, nil)

	resp, _ := adminRequest(t, nil, http.MethodGet, ts.URL+"/api/admin/events", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without PIN, got %d", resp.StatusCode)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, body := adminRequest(t, client, http.MethodPost, ts.URL+"/api/admin/login", "", map[string]string{"pin": "2026"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login expected 200, got %d: %s", resp.StatusCode, body)
	}
	var hostCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == handlers.HostCookieName {
			hostCookie = c
		}
	}
	if hostCookie == nil || !hostCookie.HttpOnly || hostCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("expected HttpOnly SameSite=Strict host cookie, got %+v", hostCookie)
	}
	resp, _ = adminRequest(t, client, http.MethodGet, ts.URL+"/api/admin/session", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session with cookie expected 200, got %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/admin/session", nil)
	req.AddCookie(&http.Cookie{Name: handlers.HostCookieName, Value: "9999999999.deadbeef"})
	forged, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("forged request: %v", err)
	}
	forged.Body.Close()
	if forged.StatusCode != http.StatusUnauthorized {
		t.Fatalf("forged cookie expected 401, got %d", forged.StatusCode)
	}

	resp, _ = adminRequest(t, client, http.MethodPost, ts.URL+"/api/admin/logout", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logout expected 200, got %d", resp.StatusCode)
	}
	resp, _ = adminRequest(t, client, http.MethodGet, ts.URL+"/api/admin/session", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after logout expected 401, got %d", resp.StatusCode)
	}

	for i := 1; i <= 4; i++ {
		resp, _ = adminRequest(t, nil, http.MethodPost, ts.URL+"/api/admin/login", "", map[string]string{"pin": "0000"})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("wrong PIN #%d expected 401, got %d", i, resp.StatusCode)
		}
	}
	resp, _ = adminRequest(t, nil, http.MethodPost, ts.URL+"/api/admin/login", "", map[string]string{"pin": "0000"})
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("5th wrong PIN expected 429 with Retry-After, got %d", resp.StatusCode)
	}
	resp, _ = adminRequest(t, nil, http.MethodPost, ts.URL+"/api/admin/login", "", map[string]string{"pin": "2026"})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("right PIN during lockout expected 429, got %d", resp.StatusCode)
	}
}

func TestAdminCreateListUpdateEvents(t *testing.T) {
	ts, api, _, firstRoom := setupTestServer(t, nil)

	// Validation
	resp, body := adminRequest(t, nil, http.MethodPost, ts.URL+"/api/admin/events", "2026", map[string]any{"title": "  ", "maxPhotosPerPerson": 0})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), `"title"`) || !strings.Contains(string(body), `"maxPhotosPerPerson"`) {
		t.Fatalf("invalid create expected 400 with field errors, got %d %s", resp.StatusCode, body)
	}
	resp, _ = adminRequest(t, nil, http.MethodPost, ts.URL+"/api/admin/events", "2026", map[string]any{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create without title expected 400, got %d", resp.StatusCode)
	}

	// Create
	resp, body = adminRequest(t, nil, http.MethodPost, ts.URL+"/api/admin/events", "2026", map[string]any{
		"title":    "Batch 7 Graduation",
		"subtitle": "Twelve weeks, one wall",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create expected 201, got %d %s", resp.StatusCode, body)
	}
	var created storage.Event
	_ = json.Unmarshal(body, &created)
	if _, ok := storage.NormalizeRoomCode(created.ID); !ok || created.ID == firstRoom {
		t.Fatalf("expected a new 6-char room code, got %q", created.ID)
	}
	if !created.UploadsOpen || created.MaxPhotosPerPerson != 5 {
		t.Fatalf("expected defaults (open, 5 per person), got %+v", created)
	}

	// Guests can reach it straight away
	res, err := http.Get(roomURL(ts.URL, created.ID, "/config"))
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("new room config not reachable: %v %v", err, res)
	}
	res.Body.Close()

	// List shows both, newest first, with counts
	uploadOne(t, ts.URL, created.ID, api, "device-a", "A", "hello")
	resp, body = adminRequest(t, nil, http.MethodGet, ts.URL+"/api/admin/events", "2026", nil)
	var list struct {
		Events []storage.Event `json:"events"`
	}
	_ = json.Unmarshal(body, &list)
	if resp.StatusCode != http.StatusOK || len(list.Events) != 2 || list.Events[0].ID != created.ID || list.Events[0].PhotoCount != 1 {
		t.Fatalf("unexpected event list: %d %+v", resp.StatusCode, list.Events)
	}

	// Update: settings reach that room's wall only, without leaking the code
	sseNew, unsubNew := api.Hub.Subscribe(created.ID)
	defer unsubNew()
	sseOld, unsubOld := api.Hub.Subscribe(firstRoom)
	defer unsubOld()

	resp, body = adminRequest(t, nil, http.MethodPut, adminEventURL(ts.URL, created.ID, ""), "2026", map[string]any{
		"eventCode":          "GRAD-7",
		"maxPhotosPerPerson": 3,
	})
	var updated storage.Event
	_ = json.Unmarshal(body, &updated)
	if resp.StatusCode != http.StatusOK || updated.EventCode != "GRAD-7" || updated.MaxPhotosPerPerson != 3 || updated.Title != "Batch 7 Graduation" {
		t.Fatalf("partial update failed: %d %+v", resp.StatusCode, updated)
	}
	frame := nextFrame(t, sseNew)
	if !strings.Contains(frame, "event: config_updated") || !strings.Contains(frame, `"requiresEventCode":true`) || strings.Contains(frame, "GRAD-7") {
		t.Fatalf("unexpected config frame: %s", frame)
	}
	select {
	case f := <-sseOld:
		t.Fatalf("other room received a config frame: %s", f)
	case <-time.After(150 * time.Millisecond):
	}

	resp, body = adminRequest(t, nil, http.MethodPut, adminEventURL(ts.URL, created.ID, ""), "2026", map[string]any{"eventCode": "bad code"})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), `"eventCode"`) {
		t.Fatalf("bad event code expected 400, got %d %s", resp.StatusCode, body)
	}

	// Closing uploads blocks new photos
	resp, _ = adminRequest(t, nil, http.MethodPut, adminEventURL(ts.URL, firstRoom, ""), "2026", map[string]any{"uploadsOpen": false})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("close uploads expected 200, got %d", resp.StatusCode)
	}
	if r, b := uploadMultipartPhoto(t, ts.URL, firstRoom, "", "", "x", "", "", makeSampleJPEG(t)); r.StatusCode != http.StatusForbidden || !strings.Contains(string(b), "UPLOADS_CLOSED") {
		t.Fatalf("closed event upload expected 403 UPLOADS_CLOSED, got %d %s", r.StatusCode, b)
	}

	// Unknown room on admin routes
	resp, _ = adminRequest(t, nil, http.MethodGet, adminEventURL(ts.URL, "zzzzzz", ""), "2026", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown admin room expected 404, got %d", resp.StatusCode)
	}
}

func TestAdminModerationHideDeleteSpotlightAndReset(t *testing.T) {
	ts, api, db, room := setupTestServer(t, func(c *config.Config) { c.MaxPhotosPerPerson = 2 })
	alice1 := uploadOne(t, ts.URL, room, api, "device-alice-1111", "Alice", "first")
	alice2 := uploadOne(t, ts.URL, room, api, "device-alice-1111", "Alice", "second")

	sseCh, unsubscribe := api.Hub.Subscribe(room)
	defer unsubscribe()

	resp, body := adminRequest(t, nil, http.MethodGet, adminEventURL(ts.URL, room, "/photos"), "2026", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "device-alice-1111") {
		t.Fatalf("admin list expected device ids, got %d: %s", resp.StatusCode, body)
	}

	resp, _ = adminRequest(t, nil, http.MethodPatch, ts.URL+"/api/admin/photos/"+alice1.ID, "2026", map[string]bool{"hidden": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("hide expected 200, got %d", resp.StatusCode)
	}
	if frame := nextFrame(t, sseCh); !strings.Contains(frame, "event: photo_deleted") || !strings.Contains(frame, alice1.ID) {
		t.Fatalf("hide frame: %s", frame)
	}
	visible, _ := db.ListVisiblePhotos(room)
	if len(visible) != 1 {
		t.Fatalf("expected 1 visible photo after hide, got %d", len(visible))
	}

	resp, _ = adminRequest(t, nil, http.MethodPost, ts.URL+"/api/admin/photos/"+alice1.ID+"/spotlight", "2026", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("spotlight hidden expected 409, got %d", resp.StatusCode)
	}
	resp, _ = adminRequest(t, nil, http.MethodPatch, ts.URL+"/api/admin/photos/"+alice1.ID, "2026", map[string]bool{"hidden": false})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unhide expected 200, got %d", resp.StatusCode)
	}
	if frame := nextFrame(t, sseCh); !strings.Contains(frame, "event: photo_added") || strings.Contains(frame, "device-alice") {
		t.Fatalf("unhide frame wrong or leaks device id: %s", frame)
	}

	resp, _ = adminRequest(t, nil, http.MethodPost, ts.URL+"/api/admin/photos/"+alice2.ID+"/spotlight", "2026", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("spotlight expected 200, got %d", resp.StatusCode)
	}
	if frame := nextFrame(t, sseCh); !strings.Contains(frame, "event: photo_spotlight") || !strings.Contains(frame, alice2.ID) {
		t.Fatalf("spotlight frame: %s", frame)
	}

	resp, _ = uploadMultipartPhoto(t, ts.URL, room, api.Identity.SignDeviceID("device-alice-1111"), "198.51.100.7", "Alice", "third", "", makeSampleJPEG(t))
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 at limit, got %d", resp.StatusCode)
	}
	resp, body = adminRequest(t, nil, http.MethodPost, adminEventURL(ts.URL, room, "/devices/device-alice-1111/reset"), "2026", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reset expected 200, got %d: %s", resp.StatusCode, body)
	}
	uploadOne(t, ts.URL, room, api, "device-alice-1111", "Alice", "third after reset")
	visible, _ = db.ListVisiblePhotos(room)
	if len(visible) != 3 {
		t.Fatalf("expected 3 visible photos after reset + upload, got %d", len(visible))
	}
	resp, _ = adminRequest(t, nil, http.MethodPost, adminEventURL(ts.URL, room, "/devices/device-nobody-0000/reset"), "2026", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("reset unknown device expected 404, got %d", resp.StatusCode)
	}

	drain(sseCh)
	resp, _ = adminRequest(t, nil, http.MethodDelete, ts.URL+"/api/admin/photos/"+alice2.ID, "2026", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete expected 200, got %d", resp.StatusCode)
	}
	if frame := nextFrame(t, sseCh); !strings.Contains(frame, "event: photo_deleted") || !strings.Contains(frame, alice2.ID) {
		t.Fatalf("delete frame: %s", frame)
	}
	img, err := http.Get(ts.URL + "/api/photos/" + alice2.ID + "/image")
	if err != nil {
		t.Fatalf("get deleted image: %v", err)
	}
	img.Body.Close()
	if img.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted image expected 404, got %d", img.StatusCode)
	}
}

func TestAdminDeleteEvent(t *testing.T) {
	ts, api, db, room := setupTestServer(t, nil)
	other, _ := db.CreateEvent(storage.Event{Title: "Keep me", UploadsOpen: true})
	p := uploadOne(t, ts.URL, room, api, "device-a", "A", "gone soon")
	keep := uploadOne(t, ts.URL, other.ID, api, "device-a", "A", "stays")

	sseCh, unsubscribe := api.Hub.Subscribe(room)
	defer unsubscribe()

	resp, body := adminRequest(t, nil, http.MethodDelete, adminEventURL(ts.URL, room, ""), "2026", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"photosDeleted":1`) {
		t.Fatalf("delete event expected 200 with 1 photo, got %d %s", resp.StatusCode, body)
	}
	if frame := nextFrame(t, sseCh); !strings.Contains(frame, "event: room_deleted") {
		t.Fatalf("expected room_deleted frame, got %s", frame)
	}
	if res, _ := http.Get(roomURL(ts.URL, room, "/config")); res.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted room should 404, got %d", res.StatusCode)
	}
	if res, _ := http.Get(ts.URL + "/api/photos/" + p.ID + "/image"); res.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted event's image should 404, got %d", res.StatusCode)
	}
	if res, _ := http.Get(ts.URL + "/api/photos/" + keep.ID + "/image"); res.StatusCode != http.StatusOK {
		t.Fatalf("other event's image should remain, got %d", res.StatusCode)
	}
}

func TestAdminExportZip(t *testing.T) {
	ts, api, db, room := setupTestServer(t, nil)
	other, _ := db.CreateEvent(storage.Event{Title: "Other", UploadsOpen: true})
	first := uploadOne(t, ts.URL, room, api, "device-dewi-4444", "Dewi Ayu!", "Last standup <3")
	hidden := uploadOne(t, ts.URL, room, api, "device-dewi-4444", "Dewi Ayu!", "blurry one")
	uploadOne(t, ts.URL, room, api, "device-budi-5555", "Budi", "")
	uploadOne(t, ts.URL, other.ID, api, "device-budi-5555", "Budi", "other event photo")
	adminRequest(t, nil, http.MethodPatch, ts.URL+"/api/admin/photos/"+hidden.ID, "2026", map[string]bool{"hidden": true})

	resp, _ := adminRequest(t, nil, http.MethodGet, adminEventURL(ts.URL, room, "/export.zip"), "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("export without auth expected 401, got %d", resp.StatusCode)
	}

	resp, body := adminRequest(t, nil, http.MethodGet, adminEventURL(ts.URL, room, "/export.zip"), "2026", nil)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("export expected 200 zip, got %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, room) || !strings.Contains(cd, "-album-") {
		t.Fatalf("unexpected content-disposition: %s", cd)
	}

	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}

	firstName := "photos/001_dewi-ayu_" + first.ID[:8] + ".jpg"
	if files[firstName] == nil {
		t.Fatalf("expected %s in zip, got %v", firstName, keys(files))
	}
	if len(files) != 4 { // 2 visible photos of THIS event + memories.json + index.html
		t.Fatalf("expected 4 entries (hidden and other-event photos excluded), got %v", keys(files))
	}

	var manifest handlers.AlbumManifest
	readZipJSON(t, files["memories.json"], &manifest)
	if manifest.Count != 2 || manifest.Photos[0].ID != first.ID || manifest.Photos[0].File != firstName {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}

	html := readZipText(t, files["index.html"])
	if !strings.Contains(html, firstName) || !strings.Contains(html, "Last standup &lt;3") || strings.Contains(html, "blurry one") || strings.Contains(html, "other event photo") {
		t.Fatalf("album html missing photo, not escaped, or includes hidden/other-event photos")
	}
}

func keys(m map[string]*zip.File) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func readZipText(t *testing.T, f *zip.File) string {
	t.Helper()
	if f == nil {
		t.Fatalf("zip entry missing")
	}
	rc, err := f.Open()
	if err != nil {
		t.Fatalf("open zip entry: %v", err)
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	return string(data)
}

func readZipJSON(t *testing.T, f *zip.File, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(readZipText(t, f)), v); err != nil {
		t.Fatalf("decode zip json: %v", err)
	}
}
