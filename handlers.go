package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
)

var tmplFuncs = template.FuncMap{
	"json": func(v any) template.JS {
		b, err := json.Marshal(v)
		if err != nil {
			return template.JS("[]")
		}
		return template.JS(b)
	},
}

type ctxKey string

const lakeSlugKey ctxKey = "lakeSlug"

// withLake pins a literal lake slug onto the request.
//
// Lake routes are registered per-slug at boot rather than as "/{lake}/...".
// A root-level wildcard segment can never be disambiguated against a literal
// prefix like "/static/" — Go 1.22's mux rejects the pair outright — and lakes
// are a tiny, slow-changing set, so literal patterns are both correct and
// simpler. Adding a lake needs a restart; there is no lake-creation UI.
func withLake(slug string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h(w, r.WithContext(context.WithValue(r.Context(), lakeSlugKey, slug)))
	}
}

func lakeSlug(r *http.Request) string {
	if s, ok := r.Context().Value(lakeSlugKey).(string); ok {
		return s
	}
	return r.PathValue("lake")
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()

	sub, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/",
		cacheControl(http.FileServer(http.FS(sub)), "public, max-age=31536000, immutable")))

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	mux.HandleFunc("GET /{$}", a.lakeIndex)
	mux.HandleFunc("GET /login", a.loginForm)
	mux.HandleFunc("POST /login", a.loginSubmit)
	mux.HandleFunc("POST /logout", a.logout)

	mux.HandleFunc("GET /api/{lake}/rocks.geojson", a.geoJSON)
	mux.HandleFunc("GET /api/{lake}/rocks.csv", a.exportCSV)

	lakes, err := a.store.Lakes()
	if err != nil {
		log.Fatalf("routes: %v", err)
	}
	for _, l := range lakes {
		p := "/" + l.Slug
		mux.HandleFunc("GET "+p+"/{$}", withLake(l.Slug, a.rockList))
		mux.HandleFunc("GET "+p+"/rock/{id}/{$}", withLake(l.Slug, a.rockDetail))

		// Authoring — session required.
		mux.Handle("POST "+p+"/rock/{$}",
			a.requireAuth(withLake(l.Slug, a.rockCreate)))
		mux.Handle("POST "+p+"/rock/{id}/update",
			a.requireAuth(withLake(l.Slug, a.rockUpdate)))
		mux.Handle("POST "+p+"/rock/{id}/move",
			a.requireAuth(withLake(l.Slug, a.rockMove)))
		mux.Handle("POST "+p+"/rock/{id}/delete",
			a.requireAuth(withLake(l.Slug, a.rockDelete)))
	}

	return logRequests(mux)
}

func cacheControl(h http.Handler, v string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", v)
		h.ServeHTTP(w, r)
	})
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			log.Printf("%s %s", r.Method, r.URL.Path)
		}
		h.ServeHTTP(w, r)
	})
}

func (a *App) requireAuth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.auth.IsAuthed(r) {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) render(w http.ResponseWriter, name string, data map[string]any) {
	data["V"] = a.assetV
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("ERROR render %s: %v", name, err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// --- public pages --------------------------------------------------------

func (a *App) lakeIndex(w http.ResponseWriter, r *http.Request) {
	lakes, err := a.store.Lakes()
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	// One lake is the whole product; don't make people click through to it.
	if len(lakes) == 1 {
		http.Redirect(w, r, "/"+lakes[0].Slug+"/", http.StatusFound)
		return
	}
	a.render(w, "lake_list.html", map[string]any{
		"Title": "Lakes", "Lakes": lakes, "Authed": a.auth.IsAuthed(r)})
}

func (a *App) lakeOr404(w http.ResponseWriter, r *http.Request) *Lake {
	lake, err := a.store.LakeBySlug(lakeSlug(r))
	if err != nil {
		http.NotFound(w, r)
		return nil
	}
	return lake
}

func (a *App) rockList(w http.ResponseWriter, r *http.Request) {
	lake := a.lakeOr404(w, r)
	if lake == nil {
		return
	}
	rocks, err := a.store.RocksByLake(lake.ID)
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	for i := range rocks {
		rocks[i].URL = "/" + lake.Slug + "/rock/" + strconv.FormatInt(rocks[i].ID, 10) + "/"
	}
	a.render(w, "rock_list.html", map[string]any{
		"Title": lake.Name, "Lake": lake, "Rocks": rocks, "Authed": a.auth.IsAuthed(r)})
}

func (a *App) rockDetail(w http.ResponseWriter, r *http.Request) {
	lake := a.lakeOr404(w, r)
	if lake == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rock, err := a.store.Rock(id)
	if err != nil || rock.LakeID != lake.ID {
		http.NotFound(w, r)
		return
	}
	rock.URL = "/" + lake.Slug + "/rock/" + strconv.FormatInt(rock.ID, 10) + "/"
	a.render(w, "rock_detail.html", map[string]any{
		"Title": rock.MarkerID, "Lake": lake, "Rock": rock,
		"Rocks": []Rock{*rock}, "Authed": a.auth.IsAuthed(r)})
}

// --- the open dataset ----------------------------------------------------

// geoJSON is the point of the project: the hazard dataset as a cacheable,
// CORS-open endpoint anyone can consume.
//
// `dedication` is deliberately NOT emitted here. Those are real people's names
// and two memorials; they render on the site but stay out of the bulk download.
// Ruled 2026-08-31.
func (a *App) geoJSON(w http.ResponseWriter, r *http.Request) {
	lake, err := a.store.LakeBySlug(lakeSlug(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rocks, err := a.store.RocksByLake(lake.ID)
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}

	type feature struct {
		Type     string         `json:"type"`
		Geometry map[string]any `json:"geometry"`
		Props    map[string]any `json:"properties"`
	}
	feats := make([]feature, 0, len(rocks))
	for _, rk := range rocks {
		if rk.Latitude == nil || rk.Longitude == nil {
			continue // a rock with no position is not a mappable hazard
		}
		p := map[string]any{
			"marker_id": rk.MarkerID,
			"nickname":  rk.Nickname,
			"status":    rk.Status,
		}
		if rk.LocalName != "" && rk.LocalName != rk.Nickname {
			p["local_name"] = rk.LocalName
		}
		if rk.SizeM != nil {
			p["size_m"] = *rk.SizeM
		}
		if rk.DepthFt != nil {
			p["depth_ft"] = *rk.DepthFt
		}
		if rk.Source != "" {
			p["source"] = rk.Source
		}
		feats = append(feats, feature{
			Type: "Feature",
			Geometry: map[string]any{"type": "Point",
				"coordinates": []float64{*rk.Longitude, *rk.Latitude}},
			Props: p,
		})
	}

	out := map[string]any{
		"type": "FeatureCollection",
		"properties": map[string]any{
			"name":    lake.Name + " rock hazards",
			"steward": "Eagle Lake Property Owners Association (ELPOA)",
			"region":  "Frontenac County, Ontario, Canada",
			"bbox": map[string]float64{"min_lat": lake.MinLat, "max_lat": lake.MaxLat,
				"min_lng": lake.MinLng, "max_lng": lake.MaxLng},
			"license": "CC BY 4.0",
			"warning": "These points mark BOAT HAZARDS. Positions are as-recorded by " +
				"ELPOA volunteers, not a hydrographic survey. Do not navigate by them.",
		},
		"features": feats,
	}

	w.Header().Set("Content-Type", "application/geo+json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=300")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		log.Printf("ERROR geojson encode: %v", err)
	}
}

func (a *App) exportCSV(w http.ResponseWriter, r *http.Request) {
	lake := a.lakeOr404(w, r)
	if lake == nil {
		return
	}
	rocks, err := a.store.RocksByLake(lake.ID)
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+lake.Slug+`-rocks.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write([]string{"marker_id", "nickname", "latitude", "longitude",
		"size_m", "depth_ft", "status"})
	for _, rk := range rocks {
		f := func(p *float64) string {
			if p == nil {
				return ""
			}
			return strconv.FormatFloat(*p, 'f', -1, 64)
		}
		size := ""
		if rk.SizeM != nil {
			size = strconv.Itoa(*rk.SizeM)
		}
		_ = cw.Write([]string{rk.MarkerID, rk.Nickname, f(rk.Latitude), f(rk.Longitude),
			size, f(rk.DepthFt), rk.Status})
	}
}

// --- authoring -----------------------------------------------------------

func (a *App) loginForm(w http.ResponseWriter, r *http.Request) {
	a.render(w, "login.html", map[string]any{"Title": "Sign in", "Next": r.URL.Query().Get("next")})
}

func (a *App) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", 400)
		return
	}
	if !a.auth.Check(r.PostFormValue("password")) {
		w.WriteHeader(http.StatusUnauthorized)
		a.render(w, "login.html", map[string]any{"Title": "Sign in", "Error": "Incorrect password."})
		return
	}
	a.auth.Issue(w, a.secure)
	next := r.PostFormValue("next")
	if !strings.HasPrefix(next, "/") {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusFound)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	a.auth.Clear(w)
	http.Redirect(w, r, "/", http.StatusFound)
}

func parseFloatPtr(s string) *float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}

func parseIntPtr(s string) *int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &v
}

func (a *App) rockFromForm(r *http.Request, rk *Rock) {
	rk.MarkerID = strings.TrimSpace(r.PostFormValue("marker_id"))
	rk.Nickname = strings.TrimSpace(r.PostFormValue("nickname"))
	rk.LocalName = strings.TrimSpace(r.PostFormValue("local_name"))
	rk.Latitude = parseFloatPtr(r.PostFormValue("latitude"))
	rk.Longitude = parseFloatPtr(r.PostFormValue("longitude"))
	rk.SizeM = parseIntPtr(r.PostFormValue("size_m"))
	rk.DepthFt = parseFloatPtr(r.PostFormValue("depth_ft"))
	rk.Dedication = strings.TrimSpace(r.PostFormValue("dedication"))
	if s := strings.TrimSpace(r.PostFormValue("status")); s != "" {
		rk.Status = s
	}
}

func (a *App) rockCreate(w http.ResponseWriter, r *http.Request) {
	lake := a.lakeOr404(w, r)
	if lake == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", 400)
		return
	}
	rk := Rock{LakeID: lake.ID, Status: "candidate", Source: "added via site"}
	a.rockFromForm(r, &rk)
	if rk.MarkerID == "" {
		http.Error(w, "marker_id is required", 400)
		return
	}
	if err := a.store.CreateRock(&rk); err != nil {
		http.Error(w, "could not create: "+err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/"+lake.Slug+"/", http.StatusFound)
}

func (a *App) rockUpdate(w http.ResponseWriter, r *http.Request) {
	lake := a.lakeOr404(w, r)
	if lake == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rk, err := a.store.Rock(id)
	if err != nil || rk.LakeID != lake.ID {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", 400)
		return
	}
	a.rockFromForm(r, rk)
	if err := a.store.UpdateRock(rk); err != nil {
		http.Error(w, "could not update: "+err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/"+lake.Slug+"/rock/"+strconv.FormatInt(id, 10)+"/", http.StatusFound)
}

// rockMove backs drag-to-reposition on the map. JSON in, JSON out.
func (a *App) rockMove(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"ok":false,"error":"bad json"}`, 400)
		return
	}
	if err := a.store.MoveRock(id, body.Latitude, body.Longitude); err != nil {
		http.Error(w, `{"ok":false}`, 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (a *App) rockDelete(w http.ResponseWriter, r *http.Request) {
	lake := a.lakeOr404(w, r)
	if lake == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := a.store.DeleteRock(id); err != nil {
		http.Error(w, "could not delete", 500)
		return
	}
	http.Redirect(w, r, "/"+lake.Slug+"/", http.StatusFound)
}
