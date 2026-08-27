# Letterboxd data

Drop your Letterboxd export CSVs here (Settings → Data → Export in the
Letterboxd app). These files hold your personal watch history and are
`.gitignore`d — never commit them.

Recognized filenames (from `internal/letterboxd/letterboxd.go`), all optional:

- `watched.csv`
- `watchlist.csv`
- `ratings.csv`

Any missing file is treated as "no data" rather than an error. Set
`LETTERBOXD_DATA_DIR` in `.env` to point elsewhere instead of using this
directory.
