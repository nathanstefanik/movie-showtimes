# Movie Showtimes

Scrapes NYC repertory/arthouse theater showtimes into a two-week grid,
enriched with TMDB metadata and your Letterboxd watch history.

## Setup

```sh
cp .env.example .env   # fill in ADMIN_TOKEN / TMDB_API_KEY; both optional but recommended
go run .
```

Then open http://localhost:8080.

- **`ADMIN_TOKEN`** gates `/api/refresh` and the theater-list mutations. Without
  it those endpoints fail closed (503). Generate one with `openssl rand -hex 32`.
- **`TMDB_API_KEY`** ([get one here](https://www.themoviedb.org/settings/api))
  adds posters, overviews, and directors to the day view. The app works
  without it — that metadata is just blank.
- Drop Letterboxd export CSVs into `data/movies/` to color film titles by
  watched/watchlist/rating status — see `data/movies/README.md`. Override the
  directory with `LETTERBOXD_DATA_DIR` if needed.

## How it works

Each theater in `data/theaters.json` has a parser in `internal/scraper/`
(or the parser is inferred from the site hostname when a theater is added
in the UI). A refresh writes `data/showtimes.json`; the UI shows today
through 13 days out in `America/New_York`, one week at a time.

Title colors (watchlist = blue, watched = grey, 4★+ = green, 2★ or less =
red) come from the Letterboxd CSVs. Day-view posters come from TMDB.

## HTTP

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| GET | `/` | no | App |
| GET | `/api/showtimes` | no | Grid + theater status |
| POST | `/api/refresh` | admin | Re-scrape + TMDB refresh |
| GET | `/api/theaters` | no | Theater list |
| POST | `/api/theaters` | admin | Add a theater |
| DELETE | `/api/theaters/{id}` | admin | Remove a theater |

Admin auth is the `X-Admin-Token` header, or `Authorization: Bearer`.

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

The systemd units in the repo root assume the binary, `.env`, and `data/`
live in `/opt/movie-showtimes` as user `moviedash`:

- `movie-showtimes.service` — HTTP server on `:8080`
- `movie-showtimes-refresh.service` — `POST /api/refresh` via curl
- `movie-showtimes-refresh.timer` — daily at 06:00

A local `deploy.sh` (gitignored) can copy those files onto a host; it is
not part of the app.
