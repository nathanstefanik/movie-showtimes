# Movie Showtimes

Scrapes NYC repertory/arthouse theater showtimes into a weekly grid, enriched
with TMDB metadata and your own Letterboxd watch history.

## Setup

```sh
cp .env.example .env   # fill in ADMIN_TOKEN / TMDB_API_KEY; both optional but recommended
go run .
```

Then open http://localhost:8080.

- **`ADMIN_TOKEN`** gates `/api/refresh` and the theater-list mutations. Without
  it those endpoints fail closed (503) rather than staying open. Generate one
  with `openssl rand -hex 32`.
- **`TMDB_API_KEY`** ([get one here](https://www.themoviedb.org/settings/api))
  adds posters, overviews, and directors to the day view. The app works
  without it — that metadata is just blank.
- Drop Letterboxd export CSVs into `data/movies/` to color film titles by
  watched/watchlist/rating status — see `data/movies/README.md`.

## Data & secrets

Nothing sensitive is meant to be committed to this repo:

- `.env` (your API keys and admin token) is gitignored. `.env.example` shows
  the shape and is the only one tracked.
- `data/movies/*.csv` (your personal Letterboxd history) is gitignored.
- `data/showtimes.json` and `data/tmdb.json` are runtime caches, rebuilt by
  the app; gitignored.
- `data/theaters.json` (theater names/URLs/parsers) is app config, not a
  secret, and stays tracked.

## Deploying

`./deploy.sh` ships the binary plus `.env` and `data/` to the target host over
SSH — see comments in the script for the exact layout expected there.
