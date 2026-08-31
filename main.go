// EagleLakeRocks — a map of marked navigation hazards on Eagle Lake, Ontario,
// for the Eagle Lake Property Owners Association.
//
// Single static binary + SQLite on a persistent volume. No framework: the whole
// point is that this survives years of neglect on a public repo.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

//go:embed seed/eagle_lake_rocks.geojson
var seedFS embed.FS

type App struct {
	store *Store
	auth  *Auth
	tmpl  *template.Template
	// assetV busts the static cache on deploy. Static assets are served with a
	// long max-age, so without a version in the URL a redeploy would leave
	// browsers running the previous build's JS and CSS against the new HTML.
	assetV string
	// secure marks cookies Secure; off only for plain-HTTP local runs.
	secure bool
}

func main() {
	hash := flag.String("hash", "", "print a bcrypt hash for the given password and exit")
	flag.Parse()
	if *hash != "" {
		h, err := HashPassword(*hash)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(h)
		return
	}

	dataDir := env("DATA_DIR", "/data")
	addr := ":" + env("PORT", "8080")

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("data dir: %v", err)
	}
	store, err := OpenStore(filepath.Join(dataDir, "rocks.sqlite3"))
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// Seed from the embedded public dataset on first boot only.
	if store.count() == 0 {
		raw, err := seedFS.ReadFile("seed/eagle_lake_rocks.geojson")
		if err != nil {
			log.Fatalf("read seed: %v", err)
		}
		tmp := filepath.Join(dataDir, ".seed.geojson")
		if err := os.WriteFile(tmp, raw, 0o600); err != nil {
			log.Fatalf("stage seed: %v", err)
		}
		if err := store.Seed(tmp); err != nil {
			log.Fatalf("seed: %v", err)
		}
		os.Remove(tmp)
		log.Printf("seeded %d rocks from the embedded public dataset", store.count())
	}

	// Private dedications, if an operator has placed them on the volume.
	// Absent file is normal and silent-by-design for public clones.
	if n, err := store.ApplyPrivateOverlay(filepath.Join(dataDir, "dedications.json")); err != nil {
		log.Printf("WARN private overlay: %v", err)
	} else if n > 0 {
		log.Printf("applied %d private dedications", n)
	}

	app := &App{
		store:  store,
		auth:   NewAuth(),
		assetV: buildFingerprint(),
		tmpl:   template.Must(template.New("").Funcs(tmplFuncs).ParseFS(templateFS, "templates/*.html")),
		secure: env("COOKIE_SECURE", "1") == "1",
	}
	if !app.auth.enabled {
		log.Printf("WARN ADMIN_PASSWORD_HASH unset — editing is disabled, site is read-only")
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("EagleLakeRocks listening on %s (%d rocks)", addr, store.count())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Print("shut down cleanly")
}

// buildFingerprint identifies this build. It uses the executable's mtime, which
// changes on every deploy and is stable across a suspend/resume cycle.
func buildFingerprint() string {
	exe, err := os.Executable()
	if err != nil {
		return "dev"
	}
	fi, err := os.Stat(exe)
	if err != nil {
		return "dev"
	}
	return strconv.FormatInt(fi.ModTime().Unix(), 36)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
