package handlers

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/photo-share/backend/storage"
)

// AlbumPhoto is one entry in memories.json and the offline album.
type AlbumPhoto struct {
	Index     int       `json:"index"`
	File      string    `json:"file"`
	ID        string    `json:"id"`
	Nickname  string    `json:"nickname"`
	Caption   string    `json:"caption"`
	CreatedAt time.Time `json:"createdAt"`
}

// AlbumManifest is written as memories.json at the root of the export.
type AlbumManifest struct {
	Title      string       `json:"title"`
	Subtitle   string       `json:"subtitle"`
	ExportedAt time.Time    `json:"exportedAt"`
	Count      int          `json:"count"`
	Photos     []AlbumPhoto `json:"photos"`
	// Missing lists photo IDs whose image could not be read from storage during export.
	Missing []string `json:"missing,omitempty"`
}

// handleAdminExport streams a ZIP with every visible photo (oldest first), memories.json, and an offline index.html.
// Hidden photos are left out: the host already moderated them off the wall.
func (a *API) handleAdminExport(w http.ResponseWriter, r *http.Request, ev storage.Event) {
	all, err := a.DB.ListAllPhotos(ev.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list photos")
		return
	}

	visible := make([]storage.Photo, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- { // ListAllPhotos is newest first; the album reads oldest first
		if !all[i].Hidden {
			visible = append(visible, all[i])
		}
	}

	exportedAt := time.Now().UTC()
	filename := fmt.Sprintf("%s-%s-album-%s.zip", slugify(ev.Title), ev.ID, exportedAt.Format("2006-01-02"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	zw := zip.NewWriter(w)
	defer func() {
		if err := zw.Close(); err != nil {
			log.Printf("export: closing zip: %v", err)
		}
	}()

	manifest := AlbumManifest{
		Title:      ev.Title,
		Subtitle:   ev.Subtitle,
		ExportedAt: exportedAt,
		Photos:     make([]AlbumPhoto, 0, len(visible)),
	}

	for i, p := range visible {
		if r.Context().Err() != nil {
			return // client gave up on the download
		}
		index := i + 1
		file := fmt.Sprintf("photos/%03d_%s_%s%s", index, slugify(p.Nickname), shortID(p.ID), photoExt(p))

		if err := copyPhotoIntoZip(r, a.Objects, zw, p, file); err != nil {
			log.Printf("export: skipping photo %s: %v", p.ID, err)
			manifest.Missing = append(manifest.Missing, p.ID)
			continue
		}
		manifest.Photos = append(manifest.Photos, AlbumPhoto{
			Index:     index,
			File:      file,
			ID:        p.ID,
			Nickname:  p.Nickname,
			Caption:   p.Caption,
			CreatedAt: p.CreatedAt.UTC(),
		})
	}
	manifest.Count = len(manifest.Photos)

	if err := writeZipJSON(zw, "memories.json", manifest); err != nil {
		log.Printf("export: memories.json: %v", err)
		return
	}
	if err := writeAlbumHTML(zw, manifest); err != nil {
		log.Printf("export: index.html: %v", err)
	}
}

func copyPhotoIntoZip(r *http.Request, objects storage.PhotoObjectStore, zw *zip.Writer, p storage.Photo, file string) error {
	reader, _, err := objects.GetPhoto(r.Context(), p.ObjectKey)
	if err != nil {
		return err
	}
	defer reader.Close()

	// Photos are already compressed JPEG/PNG/WebP, so store them as-is instead of deflating again.
	header := &zip.FileHeader{Name: file, Method: zip.Store, Modified: p.CreatedAt}
	dst, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, reader)
	return err
}

func writeZipJSON(zw *zip.Writer, name string, v any) error {
	dst, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(dst)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeAlbumHTML(zw *zip.Writer, m AlbumManifest) error {
	dst, err := zw.CreateHeader(&zip.FileHeader{Name: "index.html", Method: zip.Deflate, Modified: time.Now()})
	if err != nil {
		return err
	}
	return albumTemplate.Execute(dst, m)
}

func photoExt(p storage.Photo) string {
	if ext := strings.ToLower(path.Ext(p.ObjectKey)); ext != "" {
		return ext
	}
	switch p.ContentType {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	}
	return ".jpg"
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// slugify keeps ASCII letters and digits, turns everything else into single dashes, and caps the length.
func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 24 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "guest"
	}
	return out
}

var albumTemplate = template.Must(template.New("album").Funcs(template.FuncMap{
	"tilt": func(i int) string {
		tilts := []string{"-2.2", "1.6", "-0.8", "2.4", "-1.4", "0.9", "-2.8", "1.2"}
		return tilts[i%len(tilts)]
	},
	"stamp": func(t time.Time) string { return t.UTC().Format("Jan 2, 15:04 UTC") },
	"iso":   func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body {
    margin: 0;
    background: #141210;
    color: #faf6f0;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  }
  header { max-width: 1200px; margin: 0 auto; padding: 3rem 1.5rem 1rem; }
  h1 { margin: 0; font-size: clamp(1.8rem, 4vw, 3rem); line-height: 1.1; }
  .sub { margin: .6rem 0 0; color: #d6d3d1; font-size: 1.1rem; max-width: 60ch; }
  .meta { margin: .8rem 0 0; color: #a8a29e; font-size: .9rem; }
  main {
    max-width: 1200px; margin: 0 auto; padding: 2rem 1.5rem 4rem;
    display: grid; gap: 2.2rem 1.6rem;
    grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
    list-style: none;
  }
  figure {
    margin: 0; padding: 5% 5% 0; color: #1c1917;
    background: linear-gradient(180deg, #fdfbf7 0%, #f5efe6 100%);
    border-radius: 3px;
    box-shadow: 0 14px 32px -8px rgba(0,0,0,.55), 0 2px 6px -1px rgba(0,0,0,.35);
    transform: rotate(var(--tilt));
  }
  figure a { display: block; border-radius: 2px; }
  figure a:focus-visible { outline: 3px solid #fbbf24; outline-offset: 3px; }
  img { display: block; width: 100%; aspect-ratio: 1 / 1; height: auto; object-fit: cover; object-position: 50% 35%; background: #2a2622; }
  figcaption { padding: .7rem .2rem .9rem; }
  .cap { margin: 0 0 .5rem; font-family: "Caveat", "Bradley Hand", "Segoe Print", "Comic Sans MS", cursive; font-size: 1.35rem; line-height: 1.1; color: #292524; overflow-wrap: anywhere; }
  .who { margin: 0; display: flex; justify-content: space-between; gap: .5rem; font-size: .8rem; color: #57534e; }
  .who b { color: #44403c; }
  .empty { grid-column: 1 / -1; color: #d6d3d1; }
  @media (prefers-reduced-motion: no-preference) {
    figure { transition: transform .2s ease; }
    figure:hover { transform: rotate(0deg) scale(1.02); }
  }
</style>
</head>
<body>
<header>
  <h1>{{.Title}}</h1>
  {{if .Subtitle}}<p class="sub">{{.Subtitle}}</p>{{end}}
  <p class="meta">{{.Count}} {{if eq .Count 1}}memory{{else}}memories{{end}}, exported <time datetime="{{iso .ExportedAt}}">{{stamp .ExportedAt}}</time></p>
</header>
<main>
{{range $i, $p := .Photos}}
  <figure style="--tilt: {{tilt $i}}deg">
    <a href="{{$p.File}}"><img src="{{$p.File}}" alt="{{if $p.Caption}}{{$p.Caption}}, shared by {{$p.Nickname}}{{else}}Photo shared by {{$p.Nickname}}{{end}}" width="600" height="600" loading="lazy"></a>
    <figcaption>
      {{if $p.Caption}}<p class="cap">{{$p.Caption}}</p>{{end}}
      <p class="who"><b>@{{$p.Nickname}}</b><time datetime="{{iso $p.CreatedAt}}">{{stamp $p.CreatedAt}}</time></p>
    </figcaption>
  </figure>
{{else}}
  <p class="empty">No photos were on the wall when this album was exported.</p>
{{end}}
</main>
<script>
  // Show times in the viewer's own timezone; the UTC text stays as the fallback.
  for (const el of document.querySelectorAll('time[datetime]')) {
    const d = new Date(el.getAttribute('datetime'));
    if (!isNaN(d)) el.textContent = d.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
  }
</script>
</body>
</html>
`))
