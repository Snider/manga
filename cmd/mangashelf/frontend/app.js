// SPDX-License-Identifier: EUPL-1.2
"use strict";

const $ = (id) => document.getElementById(id);
const enc = encodeURIComponent;

// el("div", {class: "x", onclick: f}, "text", child…): never innerHTML, so
// a title from MangaDex can't turn into markup.
function el(tag, attrs = {}, ...kids) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === null || v === undefined || v === false) continue;
    if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
    else if (k === "class") n.className = v;
    else n.setAttribute(k, v === true ? "" : v);
  }
  for (const k of kids.flat()) if (k !== null && k !== undefined && k !== false) n.append(k);
  return n;
}

async function api(path, body) {
  const opts = body === undefined ? {} : { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) };
  const r = await fetch("/api/" + path, opts);
  if (r.status === 204) return null;
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(data.error || r.statusText);
  return data;
}

let toastTimer;
function toast(msg, bad = false) {
  const t = $("toast");
  t.textContent = msg;
  t.className = "toast" + (bad ? " bad" : "");
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (t.hidden = true), bad ? 8000 : 3500);
}
const oops = (e) => toast(e.message || String(e), true);

// --- tabs ------------------------------------------------------------------
function show(tab) {
  document.querySelectorAll("nav button").forEach((b) => b.classList.toggle("on", b.dataset.tab === tab));
  document.querySelectorAll(".tab").forEach((s) => s.classList.toggle("on", s.id === tab));
  if (tab === "shelf") loadShelf();
  if (tab === "jobs") loadJobs();
  if (tab === "settings") loadSettings();
  if (tab === "find") $("q").focus();
}
document.addEventListener("click", (e) => {
  const tab = e.target.closest("[data-tab],[data-go]");
  if (tab) { e.preventDefault(); show(tab.dataset.tab || tab.dataset.go); return; }
  const ext = e.target.closest("a[data-ext]");
  if (ext) { e.preventDefault(); api("browse", { url: ext.href }).catch(oops); }
});

// --- the bookcase ----------------------------------------------------------
let shelf = { series: [] };

async function loadShelf() {
  try {
    shelf = await api("shelf");
  } catch (e) { return oops(e); }
  $("empty-folder").textContent = shelf.library;
  drawShelf();
}

function shown() {
  const q = $("filter").value.trim().toLowerCase();
  let list = shelf.series.filter((s) => !q || s.title.toLowerCase().includes(q) || s.folder.toLowerCase().includes(q));
  const by = $("sort").value;
  const lastRead = (s) => Math.max(0, ...Object.values(s.progress || {}).map((p) => p.at));
  if (by === "title") list = [...list].sort((a, b) => a.title.localeCompare(b.title, undefined, { numeric: true }));
  if (by === "reading") list = [...list].sort((a, b) => lastRead(b) - lastRead(a));
  return list;
}

// reading: what to carry on with, most recently read first. A book she's
// part-way through, or (when she finished one) the next one in the series.
function reading() {
  const out = [];
  for (const s of shelf.series) {
    let last = null;
    for (const b of s.books) {
      const p = s.progress[b.file];
      if (p && (!last || p.at > last.p.at)) last = { b, p };
    }
    if (!last) continue;
    if (last.p.page < last.p.pages - 1) {
      out.push({ s, b: last.b, at: last.p.at, page: last.p.page, pages: last.p.pages });
      continue;
    }
    const next = s.books[s.books.indexOf(last.b) + 1];
    if (next && !s.progress[next.file]) out.push({ s, b: next, at: last.p.at, page: 0, pages: 0 });
  }
  return out.sort((a, b) => b.at - a.at).slice(0, 10);
}

function drawContinue() {
  const items = $("filter").value.trim() ? [] : reading();
  $("continue").hidden = items.length === 0;
  $("continue-row").replaceChildren(...items.map(({ s, b, page, pages }) => {
    const where = pages ? b.name + " · page " + (page + 1) + " of " + pages : "Next: " + b.name;
    const pct = pages ? Math.round((100 * (page + 1)) / pages) : 0;
    return el("button", { class: "cont", title: s.title + " · " + where, onclick: () => openBook(s, b) },
      s.hasCover ? el("img", { src: "/api/cover/" + enc(s.folder), alt: "" }) : el("div", { class: "nocover" }),
      el("div", { class: "info" },
        el("div", { class: "t" }, s.title),
        el("div", { class: "where" }, where),
        el("div", { class: "meter" }, el("i", { style: "width:" + pct + "%" })),
      ),
    );
  }));
}

function drawShelf() {
  drawContinue();
  const list = shown();
  const box = $("shelves");
  box.replaceChildren();
  $("empty").hidden = shelf.series.length > 0;
  if (!list.length) return;
  const perRow = Math.max(1, Math.floor((box.clientWidth - 36 + 20) / (140 + 20)));
  const kase = el("div", { class: "case" });
  for (let i = 0; i < list.length; i += perRow) {
    kase.append(el("div", { class: "shelf" }, list.slice(i, i + perRow).map(spine)), el("div", { class: "plank" }));
  }
  box.append(kase);
}

function spine(s) {
  const unread = s.books.length - s.read;
  const pct = s.books.length ? Math.round((100 * s.read) / s.books.length) : 0;
  const cover = s.hasCover
    ? el("img", { class: "cover", src: "/api/cover/" + enc(s.folder) + "?t=" + s.books.length, alt: "", loading: "lazy" })
    : el("div", { class: "cover nocover" }, s.title);
  return el("div", { class: "book", title: s.title, onclick: () => openSeries(s.folder) },
    cover,
    unread > 0 && s.read > 0 ? el("span", { class: "badge", title: unread + " not read yet" }, String(unread)) : null,
    el("div", { class: "label" }, s.title),
    el("div", { class: "meter", title: s.read + " of " + s.books.length + " read" }, el("i", { style: "width:" + pct + "%" })),
  );
}

$("filter").addEventListener("input", drawShelf);
$("sort").addEventListener("change", drawShelf);
addEventListener("resize", () => { if ($("shelf").classList.contains("on")) drawShelf(); });
$("open-library").onclick = () => api("open", {}).catch(oops);
$("empty-folder").onclick = (e) => { e.preventDefault(); api("open", {}).catch(oops); };
$("update-all").onclick = async () => {
  try {
    const jobs = await api("update", { folder: "" });
    toast(jobs.length ? "Checking " + jobs.length + " series for new chapters…" : "Nothing from MangaDex on the shelf yet.");
    if (jobs.length) watchJobs();
  } catch (e) { oops(e); }
};

// --- one series ------------------------------------------------------------
let current = null;

function openSeries(folder) {
  const s = shelf.series.find((x) => x.folder === folder);
  if (!s) return;
  current = s;
  $("d-cover").src = s.hasCover ? "/api/cover/" + enc(s.folder) : "";
  $("d-title").textContent = s.title;
  $("d-meta").textContent = s.books.length + (s.books.length === 1 ? " book" : " books") + " · " + s.read + " read" + (s.mangadex ? " · from MangaDex" : "");
  $("d-update").hidden = !s.mangadex;
  const next = nextBook(s);
  $("d-continue").textContent = next && s.progress[next.file] ? "Carry on: " + next.name : next ? "Read " + next.name : "Read";
  $("d-continue").disabled = !next;
  $("d-continue").onclick = () => next && openBook(s, next);
  $("d-books").replaceChildren(...s.books.map((b) => {
    const p = s.progress[b.file];
    const state = !p ? "" : p.page >= p.pages - 1 ? "✓ read" : "page " + (p.page + 1) + " of " + p.pages;
    return el("li", { onclick: () => openBook(s, b) },
      el("span", { class: "name" }, b.name),
      el("span", { class: p && p.page >= p.pages - 1 ? "done" : "dim" }, state),
      el("span", { class: "dim" }, b.format === "cbz" ? "" : b.format.toUpperCase() + " ↗"),
    );
  }));
  $("series").showModal();
}

// nextBook: the one she's part-way through, else the first unread, else the first.
function nextBook(s) {
  const readable = s.books;
  const started = readable.find((b) => s.progress[b.file] && s.progress[b.file].page < s.progress[b.file].pages - 1);
  return started || readable.find((b) => !s.progress[b.file]) || readable[0];
}

$("d-update").onclick = async () => {
  try {
    await api("update", { folder: current.folder });
    toast("Checking " + current.title + " for new chapters…");
    watchJobs();
  } catch (e) { oops(e); }
};
$("d-folder").onclick = () => api("open", { folder: current.folder }).catch(oops);
document.querySelectorAll("[data-close]").forEach((b) => (b.onclick = () => b.closest("dialog").close()));

// --- the reader ------------------------------------------------------------
const R = { series: null, book: null, page: 0, pages: 0, saveTimer: null };

async function openBook(s, b) {
  if (b.format !== "cbz") { // PDFs and EPUBs open in her own reader app
    api("open", { folder: s.folder, file: b.file }).catch(oops);
    return;
  }
  let info;
  try {
    info = await api("book/" + enc(s.folder) + "/" + enc(b.file));
  } catch (e) { return oops(e); }
  if (!info.pages) return toast(b.name + " has no pages in it.", true);
  $("series").close();
  Object.assign(R, { series: s, book: b, pages: info.pages, page: Math.min(info.page, info.pages - 1) });
  $("r-title").textContent = s.title + " · " + b.name;
  $("r-slider").max = info.pages - 1;
  $("reader").hidden = false;
  turn(0);
}

function pageURL(n) { return "/api/page/" + enc(R.series.folder) + "/" + enc(R.book.file) + "/" + n; }

function turn(by) {
  const to = R.page + by;
  if (to >= R.pages) return endOfBook();
  if (to < 0) return;
  R.page = to;
  $("r-img").src = pageURL(R.page);
  if (R.page + 1 < R.pages) new Image().src = pageURL(R.page + 1); // warm the next one
  $("r-page").textContent = R.page + 1 + " / " + R.pages;
  $("r-slider").value = R.page;
  clearTimeout(R.saveTimer);
  R.saveTimer = setTimeout(save, 400);
}

function save() {
  return api("progress", { folder: R.series.folder, file: R.book.file, page: R.page, pages: R.pages }).catch(() => {});
}

async function endOfBook() {
  await save();
  const i = R.series.books.indexOf(R.book);
  const next = R.series.books[i + 1];
  if (next && next.format === "cbz") {
    toast("On to " + next.name);
    const s = R.series;
    await openBook(s, next);
  } else {
    toast("That's the last one on the shelf." + (R.series.mangadex ? " Check for new chapters to see if there's more." : ""));
    closeReader();
  }
}

function closeReader() {
  clearTimeout(R.saveTimer);
  if (R.series) save();
  $("reader").hidden = true;
  $("r-img").removeAttribute("src");
  loadShelf();
}

const rtl = () => $("r-rtl").checked;
$("r-stage").addEventListener("click", (e) => {
  const left = e.clientX < innerWidth / 2;
  turn(left === rtl() ? 1 : -1); // manga reads right to left: the left side is "next"
});
$("r-slider").addEventListener("input", (e) => turn(Number(e.target.value) - R.page));
$("r-close").onclick = closeReader;
$("r-rtl").onchange = () => { try { localStorage.setItem("rtl", rtl() ? "1" : "0"); } catch {} };
try { if (localStorage.getItem("rtl") === "0") $("r-rtl").checked = false; } catch {}
addEventListener("keydown", (e) => {
  if ($("reader").hidden) return;
  if (e.key === "Escape") closeReader();
  else if (e.key === "ArrowLeft") turn(rtl() ? 1 : -1);
  else if (e.key === "ArrowRight") turn(rtl() ? -1 : 1);
  else if (e.key === " " || e.key === "PageDown") { e.preventDefault(); turn(1); }
  else if (e.key === "PageUp") turn(-1);
});

// --- finding manga ---------------------------------------------------------
$("search").addEventListener("submit", async (e) => {
  e.preventDefault();
  const q = $("q").value.trim();
  if (!q) return;
  const box = $("results");
  box.replaceChildren(el("p", { class: "dim" }, "Searching MangaDex…"));
  let data;
  try {
    data = await api("search?q=" + enc(q));
  } catch (e) { box.replaceChildren(); return oops(e); }
  if (!data.results.length) return box.replaceChildren(el("p", { class: "dim" }, "Nothing called that on MangaDex. Try the Japanese title, or fewer words."));
  box.replaceChildren(...data.results.map((m) => result(m, data.owned[m.id], data.language)));
});

function result(m, owned, lang) {
  const inLang = m.languages.includes(lang);
  const btn = el("button", { class: "primary go" }, owned ? "Check for new chapters" : "Put it on my shelf");
  btn.onclick = async () => {
    btn.disabled = true;
    try {
      await api("add", m);
      btn.textContent = "Downloading…";
      toast(m.title + " is on its way to the shelf.");
      watchJobs();
    } catch (e) { btn.disabled = false; oops(e); }
  };
  const facts = [m.status, m.year, m.lastVolume && "vol. " + m.lastVolume, m.authors.join(", ")].filter(Boolean).join(" · ");
  return el("div", { class: "result" },
    el("img", { src: m.cover || "", alt: "", loading: "lazy", referrerpolicy: "no-referrer" }),
    el("div", {},
      el("h3", {}, m.title),
      m.altTitle ? el("p", { class: "alt dim" }, m.altTitle) : null,
      el("div", {},
        owned ? el("span", { class: "pill ok" }, "on your shelf") : null,
        el("span", { class: "pill " + (inLang ? "ok" : "no") }, inLang ? "in your language" : "not in your language"),
      ),
      el("p", { class: "desc" }, m.description || ""),
      el("div", { class: "tags" }, facts),
      el("div", { class: "tags" }, m.tags.slice(0, 6).join(", ")),
      btn,
    ),
  );
}

// --- downloads ---------------------------------------------------------------
let jobTimer = null;
let lastDone = "";

function watchJobs() {
  if (!jobTimer) jobTimer = setInterval(loadJobs, 1000);
  loadJobs();
}

const opened = new Set();
async function loadJobs() {
  let jobs;
  try { jobs = await api("jobs"); } catch { return; }
  const busy = jobs.some((j) => j.state === "queued" || j.state === "running");
  $("busy").hidden = !busy;
  if (!busy && jobTimer) { clearInterval(jobTimer); jobTimer = null; }
  const doneNow = jobs.filter((j) => j.state === "done").map((j) => j.id + ":" + j.finished).join();
  if (doneNow !== lastDone) { lastDone = doneNow; if ($("shelf").classList.contains("on")) loadShelf(); }
  $("nojobs").hidden = jobs.length > 0;
  $("joblist").replaceChildren(...jobs.map(jobRow));
}

function jobRow(j) {
  const words = {
    queued: "Waiting its turn",
    running: j.now || "Starting…",
    done: j.pages ? "Done: " + j.pages + " new pages" : "Done: nothing new",
    failed: j.error,
    cancelled: "Stopped",
  };
  const extra = j.state === "running" && j.volumes ? " · " + j.volumes + (j.volumes === 1 ? " volume" : " volumes") + " finished" : "";
  const log = el("pre", { hidden: !opened.has(j.id) });
  if (opened.has(j.id)) fillLog(j.id, log);
  return el("div", { class: "job " + j.state },
    el("div", { class: "what" },
      el("div", { class: "title" }, j.title),
      el("div", { class: "state" }, words[j.state] + extra),
      log,
    ),
    el("button", { class: "ghost", onclick: () => { opened.has(j.id) ? opened.delete(j.id) : opened.add(j.id); loadJobs(); } }, opened.has(j.id) ? "Hide log" : "Log"),
    j.state === "queued" || j.state === "running" ? el("button", { class: "ghost", onclick: () => api("jobs/" + j.id + "/cancel", {}).then(loadJobs).catch(oops) }, "Stop") : null,
  );
}

async function fillLog(id, pre) {
  try { pre.textContent = (await api("jobs/" + id + "/log")).join("\n") || "(nothing yet)"; } catch {}
}

// --- settings ----------------------------------------------------------------
async function loadSettings() {
  let d;
  try { d = await api("settings"); } catch (e) { return oops(e); }
  const s = d.settings;
  $("s-library").value = s.library;
  $("s-language").value = s.language;
  if ($("s-language").value !== s.language) $("s-language").append(el("option", { value: s.language, selected: true }, s.language));
  $("s-format").replaceChildren(...Object.entries(d.formats).map(([v, label]) => el("option", { value: v, selected: v === s.format }, label)));
  $("s-tool").value = s.tool;
  $("tool-status").textContent = d.tool
    ? "Using " + d.tool
    : d.canFetch ? "Not downloaded yet: it's fetched (about 30 MB, once) the first time you add something." : "Not found: install it with pip install mangadex-downloader.";
}

$("pick").onclick = async () => {
  try {
    const r = await api("pick-folder", {});
    if (r.folder) $("s-library").value = r.folder;
  } catch (e) { oops(e); }
};

$("settings-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  try {
    await api("settings", { library: $("s-library").value.trim(), language: $("s-language").value, format: $("s-format").value, tool: $("s-tool").value.trim() });
    $("saved").textContent = "Saved.";
    setTimeout(() => ($("saved").textContent = ""), 2500);
  } catch (e) { oops(e); }
});

// --- start -------------------------------------------------------------------
loadShelf();
watchJobs();
