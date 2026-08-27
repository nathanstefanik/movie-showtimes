const refreshedEl = document.getElementById('refreshed-at');
const refreshBtn = document.getElementById('refresh-btn');
const tmdbBanner = document.getElementById('tmdb-banner');
const theaterList = document.getElementById('theater-list');
const addForm = document.getElementById('add-theater-form');
const theatersSection = document.getElementById('theaters-section');
const theatersToggle = document.getElementById('theaters-toggle');
const theatersPanel = document.getElementById('theaters-panel');
const theatersSummary = document.getElementById('theaters-summary');
const weekGrid = document.getElementById('week-grid');
const weekPrevBtn = document.getElementById('week-prev');
const weekNextBtn = document.getElementById('week-next');
const weekLabel = document.getElementById('week-label');
const weekViewLink = document.getElementById('week-view-link');
const todayJump = document.getElementById('today-jump');
const scheduleTitle = document.querySelector('.schedule-head h2');

let fullGrid = [];
let weekOffset = 0;
let theaterStatus = [];
let viewDay = null;
// filterGrid rebuilds every day, film and showtime object in the grid, and it
// used to run two or three times per render. Cache it and drop the cache
// whenever the grid or the hidden-theater set changes.
let filteredGridCache = null;
let theaterUrlById = new Map();

const HIDDEN_THEATERS_KEY = 'hidden-theaters';
const THEATERS_EXPANDED_KEY = 'theaters-expanded';

function loadTheatersExpandedLocal() {
  return sessionStorage.getItem(THEATERS_EXPANDED_KEY) === '1';
}

function saveTheatersExpandedLocal(expanded) {
  sessionStorage.setItem(THEATERS_EXPANDED_KEY, expanded ? '1' : '0');
}

function setTheatersExpanded(expanded) {
  theatersSection.classList.toggle('card--theaters--expanded', expanded);
  theatersPanel.classList.toggle('hidden', !expanded);
  theatersToggle.setAttribute('aria-expanded', expanded ? 'true' : 'false');
  saveTheatersExpandedLocal(expanded);
}

function updateTheatersSummary() {
  const total = theaterStatus.length;
  if (total === 0) {
    theatersSummary.textContent = 'Add a theater to get started';
    return;
  }
  const hidden = theaterStatus.filter((st) => hiddenTheaters.has(st.theater.id)).length;
  const visible = total - hidden;
  if (hidden === 0) {
    theatersSummary.textContent = total === 1 ? '1 theater' : `${total} theaters`;
    return;
  }
  theatersSummary.textContent = `${visible} of ${total} theaters visible`;
}

function syncTheatersPanel() {
  updateTheatersSummary();
  if (theaterStatus.length === 0) {
    setTheatersExpanded(true);
    return;
  }
  setTheatersExpanded(loadTheatersExpandedLocal());
}

function loadHiddenTheatersLocal() {
  try {
    return new Set(JSON.parse(localStorage.getItem(HIDDEN_THEATERS_KEY) || '[]'));
  } catch {
    return new Set();
  }
}

function saveHiddenTheatersLocal() {
  localStorage.setItem(HIDDEN_THEATERS_KEY, JSON.stringify([...hiddenTheaters]));
}

function readUrlParams() {
  return new URLSearchParams(location.search);
}

function hiddenFromUrl(params) {
  if (!params.has('hidden')) return null;
  return new Set((params.get('hidden') || '').split(',').filter(Boolean));
}

function buildUrl({ hidden = hiddenTheaters, day = viewDay, hash = location.hash } = {}) {
  const params = new URLSearchParams();
  if (hidden.size) params.set('hidden', [...hidden].join(','));
  if (day) params.set('day', day);
  const qs = params.toString();
  const base = qs ? `?${qs}` : location.pathname;
  return hash ? `${base}${hash}` : base;
}

function syncUrl(replace = false, opts = {}) {
  const url = buildUrl(opts);
  (replace ? history.replaceState : history.pushState).call(history, null, '', url);
}

function applyUrlState() {
  const params = readUrlParams();
  const fromUrl = hiddenFromUrl(params);
  hiddenTheaters = fromUrl ?? loadHiddenTheatersLocal();
  viewDay = params.get('day') || null;
  invalidateGridCache();
}

let hiddenTheaters = new Set();
applyUrlState();

function fmtRefreshed(iso) {
  if (!iso) return 'No cached data — click Refresh';
  const d = new Date(iso);
  return `Last refreshed: ${d.toLocaleString('en-US', { timeZone: 'America/New_York' })}`;
}

function badgeForStatus(st) {
  if (st.status === 'ok') {
    return '<span class="badge badge--ok">OK</span>';
  }
  if (st.status === 'no_parser') {
    return '<span class="badge badge--warn">No parser configured</span>';
  }
  const msg = st.error ? escapeHtml(st.error) : 'Error';
  return `<span class="badge badge--err" title="${msg}">Error</span>`;
}

function escapeHtml(s) {
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

// Only allow http(s) in href/src — blocks javascript:/data: XSS via theater or film URLs.
function safeHttpUrl(raw) {
  if (!raw) return '';
  try {
    const u = new URL(String(raw));
    if (u.protocol !== 'http:' && u.protocol !== 'https:') return '';
    return u.href;
  } catch {
    return '';
  }
}

const ADMIN_TOKEN_KEY = 'admin-token';

function getAdminToken() {
  return sessionStorage.getItem(ADMIN_TOKEN_KEY) || '';
}

function setAdminToken(token) {
  if (token) sessionStorage.setItem(ADMIN_TOKEN_KEY, token);
  else sessionStorage.removeItem(ADMIN_TOKEN_KEY);
}

function adminHeaders(extra = {}) {
  const headers = { ...extra };
  const token = getAdminToken();
  if (token) headers['X-Admin-Token'] = token;
  return headers;
}

async function ensureAdminToken() {
  if (getAdminToken()) return true;
  const token = window.prompt('Admin token required for this action:');
  if (!token) return false;
  setAdminToken(token.trim());
  return true;
}

async function adminFetch(url, options = {}) {
  if (!(await ensureAdminToken())) {
    throw new Error('Admin token required');
  }
  const headers = adminHeaders(options.headers || {});
  let res = await fetch(url, { ...options, headers });
  if (res.status === 401) {
    setAdminToken('');
    if (!(await ensureAdminToken())) {
      throw new Error('Admin token required');
    }
    res = await fetch(url, { ...options, headers: adminHeaders(options.headers || {}) });
  }
  return res;
}

function filmSlug(title) {
  const slug = String(title)
    .normalize('NFD')
    .replace(/[\u0300-\u036f]/g, '')
    .toLowerCase()
    .replace(/['']/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
  return slug || 'film';
}

function filmSlugs(films) {
  const counts = new Map();
  return (films || []).map((film) => {
    const base = filmSlug(film.title);
    const n = (counts.get(base) || 0) + 1;
    counts.set(base, n);
    return n === 1 ? base : `${base}-${n}`;
  });
}

function scrollToFilmHash() {
  const id = location.hash.slice(1);
  if (!id || !viewDay) return;
  const el = document.getElementById(id);
  if (el) el.scrollIntoView({ block: 'start' });
}

function filmTitleHtml(film) {
  const lbStatus = film.letterboxd_status || '';
  const titleClass = lbStatus ? ` film-card__title--${lbStatus}` : '';
  const fallback = `https://www.themoviedb.org/search/movie?query=${encodeURIComponent(film.title)}`;
  const tmdbURL = safeHttpUrl(film.tmdb_url) || fallback;
  return `<h3 class="film-card__title${titleClass}"><a href="${escapeHtml(tmdbURL)}" target="_blank" rel="noopener noreferrer">${escapeHtml(film.title)}</a></h3>`;
}

function theaterUrlForId(theaterId) {
  return theaterUrlById.get(theaterId) || '';
}

function theaterNameLink(theaterId, name, filmUrl) {
  const url = safeHttpUrl(filmUrl) || safeHttpUrl(theaterUrlForId(theaterId));
  const label = escapeHtml(name);
  if (!url) return label;
  return `<a href="${escapeHtml(url)}" target="_blank" rel="noopener noreferrer">${label}</a>`;
}

function filmDayMetaHtml(film) {
  if (!film.poster_url && !film.overview && !film.director && !film.release_year) return '';
  const posterUrl = safeHttpUrl(film.poster_url);
  const poster = posterUrl
    ? `<img class="film-card__poster" src="${escapeHtml(posterUrl)}" alt="" loading="lazy" width="92" height="138">`
    : '';
  const director = film.director
    ? `<div class="film-card__director">${escapeHtml(film.director)}</div>`
    : '';
  const year = film.release_year
    ? `<div class="film-card__year">${escapeHtml(film.release_year)}</div>`
    : '';
  const overview = film.overview
    ? `<p class="film-card__overview">${escapeHtml(film.overview)}</p>`
    : '';
  return `<div class="film-card__meta">${poster}<div class="film-card__meta-text">${director}${year}${overview}</div></div>`;
}

function renderTheaters(statusList) {
  theaterStatus = statusList || [];
  theaterUrlById = new Map(theaterStatus.map((st) => [st.theater.id, st.theater.url]));
  theaterList.innerHTML = theaterStatus.map((st) => {
    const t = st.theater;
    const hidden = hiddenTheaters.has(t.id);
    return `<li class="theater-row${hidden ? ' theater-row--hidden' : ''}">
      ${badgeForStatus(st)}
      <span class="theater-name">${escapeHtml(t.name)}</span>
      <span class="theater-url">${escapeHtml(t.url)}</span>
      <button type="button" class="btn" data-hide="${escapeHtml(t.id)}">${hidden ? 'Show' : 'Hide'}</button>
      <button type="button" class="btn btn--danger" data-delete="${escapeHtml(t.id)}">Delete</button>
    </li>`;
  }).join('');

  syncTheatersPanel();
}

// Delegated: renderTheaters replaces the whole list on every toggle, so
// per-button listeners were re-attached (and thrown away) each time.
theaterList.addEventListener('click', (e) => {
  const btn = e.target.closest('button');
  if (!btn || !theaterList.contains(btn)) return;
  if (btn.dataset.hide) {
    toggleTheaterHidden(btn.dataset.hide);
  } else if (btn.dataset.delete) {
    deleteTheater(btn.dataset.delete).catch((err) => alert(err.message));
  }
});

function toggleTheaterHidden(id) {
  if (hiddenTheaters.has(id)) hiddenTheaters.delete(id);
  else hiddenTheaters.add(id);
  invalidateGridCache();
  saveHiddenTheatersLocal();
  syncUrl();
  renderTheaters(theaterStatus);
  renderSchedule();
}

function maxWeekOffset() {
  if (!fullGrid.length) return 0;
  return Math.max(0, Math.ceil(fullGrid.length / 7) - 1);
}

function invalidateGridCache() {
  filteredGridCache = null;
}

function filteredGrid() {
  if (!filteredGridCache) filteredGridCache = filterGrid(fullGrid);
  return filteredGridCache;
}

function filterGrid(grid) {
  if (!hiddenTheaters.size) return grid;
  return grid.map((day) => ({
    ...day,
    films: (day.films || [])
      .map((film) => ({
        ...film,
        showtimes: (film.showtimes || []).filter((s) => !hiddenTheaters.has(s.theater_id)),
      }))
      .filter((film) => film.showtimes.length > 0),
  }));
}

function findDay(dateStr) {
  return filteredGrid().find((d) => d.date === dateStr);
}

function weekOffsetForDate(dateStr) {
  const idx = fullGrid.findIndex((d) => d.date === dateStr);
  if (idx < 0) return weekOffset;
  return Math.floor(idx / 7);
}

function visibleGrid() {
  const start = weekOffset * 7;
  return filteredGrid().slice(start, start + 7);
}

function updateWeekNav() {
  const days = visibleGrid();
  weekPrevBtn.disabled = weekOffset <= 0;
  weekNextBtn.disabled = weekOffset >= maxWeekOffset();
  if (days.length === 0) {
    weekLabel.textContent = weekOffset === 0 ? 'This week' : 'No dates';
    return;
  }
  weekLabel.textContent = `${days[0].label} – ${days[days.length - 1].label}`;
}

function updateDayNav() {
  const idx = fullGrid.findIndex((d) => d.date === viewDay);
  weekPrevBtn.disabled = idx <= 0;
  weekNextBtn.disabled = idx < 0 || idx >= fullGrid.length - 1;
  weekLabel.textContent = idx >= 0 ? fullGrid[idx].label : '';
}

function updateTodayJump() {
  todayJump.classList.toggle('hidden', !viewDay && weekOffset === 0);
}

function updateScheduleHead() {
  const dayMode = !!viewDay;
  weekViewLink.classList.toggle('hidden', !dayMode);
  weekGrid.classList.toggle('week-grid--day', dayMode);

  if (dayMode) {
    const day = findDay(viewDay);
    scheduleTitle.textContent = day ? `${day.label} Schedule` : 'Schedule';
    weekViewLink.href = buildUrl({ day: null, hash: '' });
    updateDayNav();
    return;
  }
  scheduleTitle.textContent = 'Schedule';
}

function weekFilmCard(film) {
  const chips = (film.showtimes || []).map((s) =>
    `<span class="chip">${escapeHtml(s.time)} · ${theaterNameLink(s.theater_id, s.theater_name, s.film_url)}</span>`
  ).join('');

  return `<article class="film-card">
    ${filmTitleHtml(film)}
    <div class="chips">${chips || '<span class="chip">—</span>'}</div>
  </article>`;
}

// Times arrive as zero-padded HH:MM, which sorts correctly as a plain string.
function compareTime(a, b) {
  return a < b ? -1 : a > b ? 1 : 0;
}

function dayFilmCard(film, slug) {
  const byTheater = new Map();
  for (const s of film.showtimes || []) {
    if (!byTheater.has(s.theater_id)) {
      byTheater.set(s.theater_id, { id: s.theater_id, name: s.theater_name, filmUrl: s.film_url, times: [] });
    }
    byTheater.get(s.theater_id).times.push(s.time);
  }

  const groups = [...byTheater.values()].map(({ id, name, times, filmUrl }) => {
    times.sort(compareTime);
    return `<div class="day-showtimes-group">
      <div class="day-showtimes-group__theater">${theaterNameLink(id, name, filmUrl)}</div>
      <div class="day-showtimes-group__times">${times.map((t) =>
        `<span class="chip chip--time">${escapeHtml(t)}</span>`
      ).join('')}</div>
    </div>`;
  }).join('');

  return `<article class="film-card film-card--day" id="${escapeHtml(slug)}">
    ${filmTitleHtml(film)}
    ${filmDayMetaHtml(film)}
    <div class="day-showtimes">${groups || '<p class="empty-day">—</p>'}</div>
  </article>`;
}

weekGrid.addEventListener('click', (e) => {
  const link = e.target.closest('[data-day-link]');
  if (!link) return;
  e.preventDefault();
  viewDay = link.dataset.dayLink;
  syncUrl(false, { hash: '' });
  renderSchedule();
});

function todayNYDate() {
  return new Intl.DateTimeFormat('en-CA', { timeZone: 'America/New_York' }).format(new Date());
}

function renderWeekGrid() {
  weekOffset = Math.min(weekOffset, maxWeekOffset());
  const visible = visibleGrid();
  updateWeekNav();

  if (visible.length === 0) {
    weekGrid.innerHTML = '<p class="empty-day">No showtimes this week.</p>';
    return;
  }

  const today = todayNYDate();
  weekGrid.innerHTML = visible.map((day) => {
    const films = (day.films || []).map(weekFilmCard).join('');
    const body = films || '<p class="empty-day">—</p>';
    const dayHref = buildUrl({ day: day.date });
    const todayClass = day.date === today ? ' day-col--today' : '';
    return `<div class="day-col${todayClass}">
      <div class="day-col__head"><a href="${escapeHtml(dayHref)}" class="day-link" data-day-link="${escapeHtml(day.date)}">${escapeHtml(day.label)}</a></div>
      <div class="day-col__body">${body}</div>
    </div>`;
  }).join('');
}

function renderDayView() {
  const day = findDay(viewDay);
  if (!day) {
    weekGrid.innerHTML = '<p class="empty-day">No showtimes for this day.</p>';
    return;
  }

  const films = (day.films || []);
  const slugs = filmSlugs(films);
  weekGrid.innerHTML = `<div class="day-col day-col--solo">
    <div class="day-col__body">${films.map((film, i) => dayFilmCard(film, slugs[i])).join('') || '<p class="empty-day">—</p>'}</div>
  </div>`;
  scrollToFilmHash();
}

function renderSchedule() {
  updateScheduleHead();
  if (viewDay) renderDayView();
  else renderWeekGrid();
  updateTodayJump();
}

// Steps by index through fullGrid, not the filtered grid, since every date is
// always present in the grid even when a day's showtimes are all hidden.
function stepDay(delta) {
  const idx = fullGrid.findIndex((d) => d.date === viewDay);
  if (idx < 0) return false;
  const next = idx + delta;
  if (next < 0 || next >= fullGrid.length) return false;
  viewDay = fullGrid[next].date;
  weekOffset = Math.floor(next / 7);
  syncUrl(false, { hash: '' });
  renderSchedule();
  return true;
}

function stepPrev() {
  if (viewDay) return stepDay(-1);
  if (weekOffset <= 0) return false;
  weekOffset -= 1;
  renderSchedule();
  return true;
}

function stepNext() {
  if (viewDay) return stepDay(1);
  if (weekOffset >= maxWeekOffset()) return false;
  weekOffset += 1;
  renderSchedule();
  return true;
}

function renderGrid(grid) {
  if (grid !== undefined) {
    fullGrid = grid || [];
    invalidateGridCache();
  }
  if (viewDay) {
    const idx = fullGrid.findIndex((d) => d.date === viewDay);
    if (idx >= 0) weekOffset = Math.floor(idx / 7);
  }
  renderSchedule();
}

function goToWeekView() {
  if (viewDay) weekOffset = weekOffsetForDate(viewDay);
  viewDay = null;
  syncUrl(false, { hash: '' });
  renderSchedule();
}

function goToToday() {
  viewDay = null;
  weekOffset = 0;
  syncUrl(false, { hash: '' });
  renderSchedule();
}

function renderPayload(data) {
  refreshedEl.textContent = fmtRefreshed(data.refreshed_at);
  tmdbBanner.classList.toggle('hidden', !!data.tmdb_configured);
  renderTheaters(data.theater_status);
  renderGrid(data.grid);
}

async function loadShowtimes() {
  const res = await fetch('/api/showtimes');
  if (!res.ok) throw new Error(await res.text());
  renderPayload(await res.json());
}

async function refreshShowtimes() {
  refreshBtn.disabled = true;
  refreshedEl.textContent = 'Refreshing showtimes and TMDB metadata…';
  try {
    const res = await adminFetch('/api/refresh', { method: 'POST' });
    if (!res.ok) throw new Error(await res.text());
    renderPayload(await res.json());
  } catch (err) {
    refreshedEl.textContent = `Refresh failed: ${err.message}`;
    throw err;
  } finally {
    refreshBtn.disabled = false;
  }
}

async function deleteTheater(id) {
  const res = await adminFetch(`/api/theaters/${encodeURIComponent(id)}`, { method: 'DELETE' });
  if (!res.ok) throw new Error(await res.text());
  hiddenTheaters.delete(id);
  saveHiddenTheatersLocal();
  syncUrl();
  await loadShowtimes();
}

theatersToggle.addEventListener('click', () => {
  setTheatersExpanded(theatersPanel.classList.contains('hidden'));
});

addForm.addEventListener('submit', async (e) => {
  e.preventDefault();
  const name = document.getElementById('theater-name').value.trim();
  const url = document.getElementById('theater-url').value.trim();
  if (!safeHttpUrl(url)) {
    alert('URL must be http or https');
    return;
  }
  try {
    const res = await adminFetch('/api/theaters', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, url }),
    });
    if (!res.ok) {
      alert(await res.text());
      return;
    }
    addForm.reset();
    await loadShowtimes();
  } catch (err) {
    alert(err.message);
  }
});

refreshBtn.addEventListener('click', () => {
  refreshShowtimes().catch((err) => alert(err.message));
});

weekPrevBtn.addEventListener('click', stepPrev);
weekNextBtn.addEventListener('click', stepNext);

weekViewLink.addEventListener('click', (e) => {
  e.preventDefault();
  goToWeekView();
});

todayJump.addEventListener('click', goToToday);

window.addEventListener('keydown', (e) => {
  if (e.ctrlKey || e.metaKey || e.altKey) return;
  const t = e.target;
  if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return;
  if (e.key === 'ArrowLeft') {
    if (stepPrev()) e.preventDefault();
  } else if (e.key === 'ArrowRight') {
    if (stepNext()) e.preventDefault();
  }
});

window.addEventListener('popstate', () => {
  applyUrlState();
  invalidateGridCache();
  renderTheaters(theaterStatus);
  renderSchedule();
});

window.addEventListener('hashchange', scrollToFilmHash);

loadShowtimes().catch((err) => {
  refreshedEl.textContent = `Failed to load: ${err.message}`;
});
