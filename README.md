# Eagle Lake Rocks

A map of marked navigation hazards on **Eagle Lake**, Frontenac County, Ontario,
maintained by the **Eagle Lake Property Owners Association (ELPOA)**.

**Live:** https://rocks.opeongo.net
**Open data:** https://rocks.opeongo.net/api/eaglelake/rocks.geojson

> ⚠️ **These points mark boat hazards.** Positions are as recorded by ELPOA
> volunteers — not a hydrographic survey. Do not navigate by them.

## The data

18 rocks. Every point carries its own provenance and accuracy in the `source`
property, because the dataset was assembled from three sources of different
quality:

| source | what it contributes |
|---|---|
| ELPOA production database, snapshot 2025-11-26 | the 13 surveyed markers, authoritative |
| Django `dumpdata`, 2025-04-05 | `depth_ft` — a migration later dropped the column, and this dump is its only surviving copy |
| Field survey, 2026-08-31 | local names, plus 5 rocks recorded since the database snapshot |

The 5 field-survey rocks carry `status: "unverified"` and have no ELPOA grid
marker ID yet. They are real reports, not confirmed markers — treat them accordingly.

The GeoJSON is served with `Access-Control-Allow-Origin: *` and is released under
**CC BY 4.0**. Sponsor and memorial dedications are shown on the site but are
deliberately **omitted from the open dataset**, so they are not bulk-harvestable.

## Running it

```bash
docker build -t eagle-lake-rocks .
docker run -p 8080:8080 -v "$PWD/data:/data" eagle-lake-rocks
```

Then open http://localhost:8080. On first boot it seeds itself from
`seed/eagle_lake_rocks.geojson` onto the volume; after that the volume is the
source of truth and a redeploy never clobbers edits made through the UI.

To enable editing, set an admin password hash:

```bash
docker run --rm eagle-lake-rocks -hash 'your-password'      # prints a bcrypt hash
```

then pass it as `ADMIN_PASSWORD_HASH`. With no hash set the site runs read-only.

### Configuration

| variable | purpose |
|---|---|
| `ADMIN_PASSWORD_HASH` | bcrypt hash; unset ⇒ read-only site |
| `SESSION_KEY` | HMAC key for session cookies; unset ⇒ random per boot (sessions drop on restart) |
| `DEDICATIONS_JSON` | private dedication text, `{"dedications":{"C1":"…"}}`; falls back to `/data/dedications.json` |
| `DATA_DIR` | default `/data` |
| `PORT` | default `8080` |

## Why it is built this way

A Go static binary on `scratch` with SQLite on a volume — about 5 MB, 256 MB of
RAM, and no runtime dependencies. The app is small (18 rocks, ~13 routes, no
background jobs) and it has to survive long stretches with nobody watching it.
Zero dependencies to rot is worth more here than framework convenience.

The Leaflet frontend is deliberately plain, dependency-free JavaScript, carried
forward from the original Django app: the marker-authoring UX — click to place,
drag to reposition — is the product, not incidental.

## History

Supersedes `sheeriot/rocks`, a Django app that ran on an Azure VM from 2024 to
2025. Rebuilt from a clean history in 2026.

## Licence

Code: MIT. Data: CC BY 4.0, © Eagle Lake Property Owners Association.
