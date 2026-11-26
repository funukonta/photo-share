package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/photo-share/backend/storage"
)

// adminRoom resolves {room} for host endpoints (same 404 as the guest API).
func (a *API) adminRoom(next roomHandler) http.HandlerFunc {
	return a.withRoom(next)
}

// eventInput is shared by create (all fields) and update (only fields sent). Pointers tell them apart.
type eventInput struct {
	Title              *string `json:"title"`
	Subtitle           *string `json:"subtitle"`
	EventCode          *string `json:"eventCode"`
	MaxPhotosPerPerson *int    `json:"maxPhotosPerPerson"`
	MaxPhotosPerIP     *int    `json:"maxPhotosPerIp"`
	UploadsOpen        *bool   `json:"uploadsOpen"`
}

// apply validates the sent fields onto ev and returns per-field errors.
func (in eventInput) apply(ev *storage.Event) map[string]string {
	fieldErrors := map[string]string{}
	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		switch {
		case title == "":
			fieldErrors["title"] = "Give the event a title."
		case utf8.RuneCountInString(title) > MaxTitleRunes:
			fieldErrors["title"] = "Keep the title under 80 characters."
		default:
			ev.Title = title
		}
	}
	if in.Subtitle != nil {
		subtitle := strings.TrimSpace(*in.Subtitle)
		if utf8.RuneCountInString(subtitle) > MaxSubtitleRunes {
			fieldErrors["subtitle"] = "Keep the subtitle under 160 characters."
		} else {
			ev.Subtitle = subtitle
		}
	}
	if in.EventCode != nil {
		code := strings.TrimSpace(*in.EventCode)
		switch {
		case utf8.RuneCountInString(code) > MaxEventCodeRunes:
			fieldErrors["eventCode"] = "Keep the event code under 40 characters."
		case strings.ContainsAny(code, " \t/?&#"):
			fieldErrors["eventCode"] = "Use letters, numbers, or dashes only, so the code survives inside the QR link."
		default:
			ev.EventCode = code
		}
	}
	if in.MaxPhotosPerPerson != nil {
		n := *in.MaxPhotosPerPerson
		if n < 1 || n > MaxPhotosPerPersonCap {
			fieldErrors["maxPhotosPerPerson"] = "Pick a limit between 1 and 50."
		} else {
			ev.MaxPhotosPerPerson = n
		}
	}
	if in.MaxPhotosPerIP != nil {
		n := *in.MaxPhotosPerIP
		if n < 1 || n > 10000 {
			fieldErrors["maxPhotosPerIp"] = "Pick a network limit between 1 and 10000."
		} else {
			ev.MaxPhotosPerIP = n
		}
	}
	if in.UploadsOpen != nil {
		ev.UploadsOpen = *in.UploadsOpen
	}
	return fieldErrors
}

func writeFieldErrors(w http.ResponseWriter, fields map[string]string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"error":  "Some settings need fixing.",
		"code":   "INVALID_SETTINGS",
		"fields": fields,
	})
}

func (a *API) handleAdminListEvents(w http.ResponseWriter, _ *http.Request) {
	events, err := a.DB.ListEvents()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list events")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (a *API) handleAdminCreateEvent(w http.ResponseWriter, r *http.Request) {
	var in eventInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}
	if in.Title == nil {
		empty := ""
		in.Title = &empty // force the "needs a title" error
	}

	ev := storage.Event{
		MaxPhotosPerPerson: a.Cfg.MaxPhotosPerPerson,
		MaxPhotosPerIP:     a.Cfg.MaxPhotosPerIP,
		UploadsOpen:        true,
	}
	if fields := in.apply(&ev); len(fields) > 0 {
		writeFieldErrors(w, fields)
		return
	}

	created, err := a.DB.CreateEvent(ev)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create event")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (a *API) handleAdminGetEvent(w http.ResponseWriter, _ *http.Request, ev storage.Event) {
	writeJSON(w, http.StatusOK, ev)
}

func (a *API) handleAdminUpdateEvent(w http.ResponseWriter, r *http.Request, ev storage.Event) {
	var in eventInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}
	if fields := in.apply(&ev); len(fields) > 0 {
		writeFieldErrors(w, fields)
		return
	}

	updated, err := a.DB.UpdateEvent(ev)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save event settings")
		return
	}
	// The event code itself never leaves the admin API; guests only learn whether one is required.
	a.Hub.Broadcast(updated.ID, EventConfigUpdated, publicConfigFrom(updated))
	writeJSON(w, http.StatusOK, updated)
}

func (a *API) handleAdminDeleteEvent(w http.ResponseWriter, r *http.Request, ev storage.Event) {
	photos, err := a.DB.DeleteEvent(ev.ID)
	if err != nil {
		if errors.Is(err, storage.ErrEventNotFound) {
			writeRoomNotFound(w)
			return
		}
		writeError(w, http.StatusInternalServerError, "Failed to delete event")
		return
	}
	for _, p := range photos {
		if err := a.Objects.DeletePhoto(r.Context(), p.ObjectKey); err != nil {
			log.Printf("delete event %s: photo file %q could not be deleted: %v", ev.ID, p.ObjectKey, err)
		}
	}
	a.Hub.Broadcast(ev.ID, EventRoomDeleted, map[string]string{"room": ev.ID})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": ev.ID, "photosDeleted": len(photos)})
}

func (a *API) handleAdminListPhotos(w http.ResponseWriter, _ *http.Request, ev storage.Event) {
	photos, err := a.DB.ListAllPhotos(ev.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list photos")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"photos": photos,
		"total":  len(photos),
	})
}

func (a *API) handleAdminResetDevice(w http.ResponseWriter, r *http.Request, ev storage.Event) {
	deviceID := strings.TrimSpace(r.PathValue("deviceId"))
	if deviceID == "" || strings.HasPrefix(deviceID, "reset:") {
		writeError(w, http.StatusBadRequest, "This phone's limit has already been reset")
		return
	}

	n, err := a.DB.ResetDeviceQuota(ev.ID, deviceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to reset the phone's limit")
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "No photos found for this phone in this event")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deviceId":      deviceID,
		"photosCounted": n,
	})
}
