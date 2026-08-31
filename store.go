package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

// Lake is a body of water with a bounding box the map fits to.
type Lake struct {
	ID     int64
	Name   string
	Slug   string
	MinLat float64
	MaxLat float64
	MinLng float64
	MaxLng float64
}

// Rock is a marked navigation hazard.
//
// Dedication holds a sponsor or memorial name. It renders on the site (as it did
// on the original ELPOA site) but is deliberately omitted from the open GeoJSON
// so the names are not bulk-harvestable. Ruled 2026-08-31.
type Rock struct {
	ID         int64    `json:"id"`
	LakeID     int64    `json:"-"`
	MarkerID   string   `json:"marker_id"`
	Nickname   string   `json:"nickname"`
	LocalName  string   `json:"local_name,omitempty"`
	Latitude   *float64 `json:"latitude"`
	Longitude  *float64 `json:"longitude"`
	SizeM      *int     `json:"size,omitempty"`
	DepthFt    *float64 `json:"depth_ft,omitempty"`
	Status     string   `json:"status"`
	Dedication string   `json:"dedication,omitempty"`
	Source     string   `json:"source,omitempty"`
	URL        string   `json:"url,omitempty"`
}

const schema = `
CREATE TABLE IF NOT EXISTS lake (
  id      INTEGER PRIMARY KEY,
  name    TEXT NOT NULL,
  slug    TEXT NOT NULL UNIQUE,
  min_lat REAL, max_lat REAL, min_lng REAL, max_lng REAL
);
CREATE TABLE IF NOT EXISTS rock (
  id         INTEGER PRIMARY KEY,
  lake_id    INTEGER NOT NULL REFERENCES lake(id) ON DELETE CASCADE,
  marker_id  TEXT NOT NULL,
  nickname   TEXT NOT NULL DEFAULT '',
  local_name TEXT NOT NULL DEFAULT '',
  latitude   REAL, longitude REAL,
  size_m     INTEGER,
  depth_ft   REAL,
  status     TEXT NOT NULL DEFAULT 'candidate',
  dedication TEXT NOT NULL DEFAULT '',
  source     TEXT NOT NULL DEFAULT '',
  UNIQUE (lake_id, marker_id)
);`

type Store struct{ db *sql.DB }

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// SQLite tolerates exactly one writer; serialising here avoids SQLITE_BUSY
	// entirely on a single-machine deployment.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Lakes() ([]Lake, error) {
	rows, err := s.db.Query(`SELECT id,name,slug,
	  COALESCE(min_lat,0),COALESCE(max_lat,0),COALESCE(min_lng,0),COALESCE(max_lng,0)
	  FROM lake ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Lake
	for rows.Next() {
		var l Lake
		if err := rows.Scan(&l.ID, &l.Name, &l.Slug, &l.MinLat, &l.MaxLat, &l.MinLng, &l.MaxLng); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) LakeBySlug(slug string) (*Lake, error) {
	var l Lake
	err := s.db.QueryRow(`SELECT id,name,slug,
	  COALESCE(min_lat,0),COALESCE(max_lat,0),COALESCE(min_lng,0),COALESCE(max_lng,0)
	  FROM lake WHERE slug=?`, slug).
		Scan(&l.ID, &l.Name, &l.Slug, &l.MinLat, &l.MaxLat, &l.MinLng, &l.MaxLng)
	if err != nil {
		return nil, err
	}
	return &l, nil
}

const rockCols = `id,lake_id,marker_id,nickname,local_name,latitude,longitude,
                  size_m,depth_ft,status,dedication,source`

func scanRock(sc interface{ Scan(...any) error }) (Rock, error) {
	var r Rock
	err := sc.Scan(&r.ID, &r.LakeID, &r.MarkerID, &r.Nickname, &r.LocalName,
		&r.Latitude, &r.Longitude, &r.SizeM, &r.DepthFt, &r.Status, &r.Dedication, &r.Source)
	return r, err
}

func (s *Store) RocksByLake(lakeID int64) ([]Rock, error) {
	rows, err := s.db.Query(`SELECT `+rockCols+` FROM rock WHERE lake_id=? ORDER BY marker_id`, lakeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rock
	for rows.Next() {
		r, err := scanRock(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Rock(id int64) (*Rock, error) {
	r, err := scanRock(s.db.QueryRow(`SELECT `+rockCols+` FROM rock WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) CreateRock(r *Rock) error {
	res, err := s.db.Exec(`INSERT INTO rock
	  (lake_id,marker_id,nickname,local_name,latitude,longitude,size_m,depth_ft,status,dedication,source)
	  VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		r.LakeID, r.MarkerID, r.Nickname, r.LocalName, r.Latitude, r.Longitude,
		r.SizeM, r.DepthFt, r.Status, r.Dedication, r.Source)
	if err != nil {
		return err
	}
	r.ID, err = res.LastInsertId()
	return err
}

func (s *Store) UpdateRock(r *Rock) error {
	_, err := s.db.Exec(`UPDATE rock SET marker_id=?,nickname=?,local_name=?,latitude=?,
	  longitude=?,size_m=?,depth_ft=?,status=?,dedication=?,source=? WHERE id=?`,
		r.MarkerID, r.Nickname, r.LocalName, r.Latitude, r.Longitude,
		r.SizeM, r.DepthFt, r.Status, r.Dedication, r.Source, r.ID)
	return err
}

// MoveRock updates only the position. Used by drag-to-reposition on the map.
func (s *Store) MoveRock(id int64, lat, lng float64) error {
	_, err := s.db.Exec(`UPDATE rock SET latitude=?,longitude=? WHERE id=?`, lat, lng, id)
	return err
}

func (s *Store) DeleteRock(id int64) error {
	_, err := s.db.Exec(`DELETE FROM rock WHERE id=?`, id)
	return err
}

func (s *Store) count() (n int) {
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM rock`).Scan(&n)
	return
}

// --- seeding -------------------------------------------------------------

type seedFile struct {
	Properties struct {
		Bbox struct {
			MinLat float64 `json:"min_lat"`
			MaxLat float64 `json:"max_lat"`
			MinLng float64 `json:"min_lng"`
			MaxLng float64 `json:"max_lng"`
		} `json:"bbox"`
	} `json:"properties"`
	Features []struct {
		Geometry struct {
			Coordinates []float64 `json:"coordinates"`
		} `json:"geometry"`
		Properties struct {
			MarkerID   *string  `json:"marker_id"`
			Nickname   *string  `json:"nickname"`
			LocalName  *string  `json:"local_name"`
			Status     string   `json:"status"`
			SizeM      *int     `json:"size_m"`
			DepthFt    *float64 `json:"depth_ft"`
			Dedication *string  `json:"dedication"`
			Source     string   `json:"source"`
		} `json:"properties"`
	} `json:"features"`
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Seed loads the recovered dataset on first boot only. It is a no-op once the
// volume holds rocks, so a redeploy never clobbers edits made through the UI.
func (s *Store) Seed(path string) error {
	if s.count() > 0 {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var sf seedFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return err
	}
	b := sf.Properties.Bbox
	res, err := s.db.Exec(`INSERT INTO lake (name,slug,min_lat,max_lat,min_lng,max_lng)
	  VALUES (?,?,?,?,?,?) ON CONFLICT(slug) DO UPDATE SET
	  min_lat=excluded.min_lat,max_lat=excluded.max_lat,
	  min_lng=excluded.min_lng,max_lng=excluded.max_lng`,
		"Eagle Lake", "eaglelake", b.MinLat, b.MaxLat, b.MinLng, b.MaxLng)
	if err != nil {
		return err
	}
	lakeID, _ := res.LastInsertId()
	if lakeID == 0 {
		if l, err := s.LakeBySlug("eaglelake"); err == nil {
			lakeID = l.ID
		}
	}
	unnamed := 0
	for _, f := range sf.Features {
		mid := deref(f.Properties.MarkerID)
		if mid == "" {
			unnamed++
			mid = fmt.Sprintf("U%d", unnamed) // field rocks with no grid label yet
		}
		nick := deref(f.Properties.Nickname)
		if nick == "" {
			nick = deref(f.Properties.LocalName)
		}
		r := Rock{LakeID: lakeID, MarkerID: mid, Nickname: nick,
			LocalName: deref(f.Properties.LocalName), Status: f.Properties.Status,
			SizeM: f.Properties.SizeM, DepthFt: f.Properties.DepthFt,
			Dedication: deref(f.Properties.Dedication), Source: f.Properties.Source}
		if len(f.Geometry.Coordinates) == 2 {
			lng, lat := f.Geometry.Coordinates[0], f.Geometry.Coordinates[1]
			r.Longitude, r.Latitude = &lng, &lat
		}
		if err := s.CreateRock(&r); err != nil {
			return fmt.Errorf("seed %s: %w", mid, err)
		}
	}
	return nil
}

// ApplyPrivateOverlay merges operator-supplied dedication text into the DB.
//
// Dedications are private records: real people's names and two memorials. They
// are NOT in this public repository and never travel in a code release. The
// overlay file lives only on the Fly volume (/data/dedications.json), pushed
// there out-of-band by an operator. Absent file = no-op, which is the correct
// behaviour for anyone who clones this repo and runs it themselves.
func (s *Store) ApplyPrivateOverlay(path string) (int, error) {
	// Preferred source: the DEDICATIONS_JSON secret. It keeps the private
	// records off disk entirely, survives loss of the volume, and needs no SSH
	// access — which matters because this ships on a scratch image with no
	// shell. The volume file is the fallback for local runs.
	raw := []byte(os.Getenv("DEDICATIONS_JSON"))
	if len(raw) == 0 {
		var err error
		raw, err = os.ReadFile(path)
		if os.IsNotExist(err) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
	}
	var ov struct {
		Dedications map[string]string `json:"dedications"`
	}
	if err := json.Unmarshal(raw, &ov); err != nil {
		return 0, err
	}
	n := 0
	for markerID, text := range ov.Dedications {
		res, err := s.db.Exec(`UPDATE rock SET dedication=? WHERE marker_id=?`, text, markerID)
		if err != nil {
			return n, err
		}
		if a, _ := res.RowsAffected(); a > 0 {
			n++
		}
	}
	return n, nil
}
