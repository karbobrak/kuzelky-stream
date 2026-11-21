"use strict";
// Match setup (tab Utkání) and scoring (tab Zápis). Uses api(), $, showTab from app.js.

let view = null; // last MatchView from the server: {zapas, vysledek, soubor, pravidla, casti, lzeZpet}

const pts = (v) => String(v).replace(".", ",");
const pinsFmt = (v) => String(v).replace(/\B(?=(\d{3})+$)/, " ");
const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);

window.loadMatch = async function (tab) {
  try { view = await api("GET", "/api/zapas"); } catch (e) { return; }
  if (tab === "utkani") { renderForm(); loadSaved(); loadRosters(); renderCka(); }
  else { renderScoring(); loadRosters(); } // rosters feed the substitution dialog
};

function applyView(v) {
  view = v;
  renderScoring();
}
window.applyMatchView = (v) => { if (v && "zapas" in v) applyView(v); };

// ================================================================== Zápis (scoring)

function renderScoring() {
  const has = !!(view && view.zapas);
  $("zNoMatch").hidden = has;
  $("zMatch").hidden = !has;
  if (!has) return;
  const m = view.zapas, r = view.vysledek;
  $("zHome").textContent = m.domaci.nazev;
  $("zAway").textContent = m.hoste.nazev;
  $("zPts").textContent = `${pts(r.dBody)} : ${pts(r.hBody)}`;
  $("zPins").textContent = `kuželky ${pinsFmt(r.dKuzelky)} : ${pinsFmt(r.hKuzelky)} · sety ${pts(r.dSety)} : ${pts(r.hSety)}` +
    (r.dohrano ? " · konec" : "") + (m.preruseno ? " · PŘERUŠENO" : "");
  window.setInterruptButtons(true, m.preruseno);
  $("btnUndo").disabled = !view.lzeZpet;

  // Part selector.
  const parts = $("zParts");
  parts.replaceChildren();
  for (let p = 1; p <= view.casti; p++) {
    const b = document.createElement("button");
    b.textContent = `Část ${p}`;
    b.className = (p === m.aktualniCast ? "on " : "") + (partDone(p) ? "done" : "");
    b.onclick = () => send("/api/zapas/cast", { cast: p });
    parts.append(b);
  }
  // Graphics mode.
  document.querySelectorAll("#zModes button").forEach((b) => b.classList.toggle("on", b.dataset.mode === m.stavGrafiky));
  if (document.activeElement !== $("zMessage")) $("zMessage").value = m.zprava || "";

  renderDuels();
  refreshGfxPreview();
  renderCkaBadge();
}

// ---- results from vysledky.kuzelky.cz
function ckaStatusText(n) {
  if (!n) return "";
  if (n.chyba) return "✗ " + n.chyba;
  return (n.stav ? `zápas ${n.stav}` : "") + (n.posledni ? ` · načteno ${n.posledni}` : "");
}
function renderCkaBadge() {
  const m = view && view.zapas, el = $("zCka");
  el.hidden = !(m && m.odkazVysledky);
  if (el.hidden) return;
  const n = view.nacitani || {};
  el.textContent = "ČKA: " + (m.nacitatVysledky ? ckaStatusText(n) || "čekám na první načtení" : "načítání vypnuto");
  el.className = "small " + (n.chyba ? "bad-text" : m.nacitatVysledky ? "ok-text" : "muted");
}
function renderCka() {
  const m = view && view.zapas;
  const linked = !!(m && m.odkazVysledky);
  if (document.activeElement !== $("ckaUrl")) $("ckaUrl").value = linked ? m.odkazVysledky : "";
  $("btnCkaLink").hidden = !m || linked;
  $("ckaLinked").hidden = !linked;
  if (!linked) return;
  $("ckaAuto").checked = !!m.nacitatVysledky;
  $("ckaAutoLabel").textContent = `Načítat výsledky automaticky (každých ${(view.nacitani || {}).interval || 60} s)`;
  $("ckaStatus").textContent = ckaStatusText(view.nacitani);
  $("ckaStatus").className = "small " + ((view.nacitani || {}).chyba ? "bad-text" : "ok-text");
}
async function ckaCall(url, body, okText) {
  const msg = $("ckaMsg");
  msg.textContent = "Načítám…"; msg.className = "small muted";
  try {
    view = await api("POST", url, body);
    renderForm(); loadSaved(); loadRosters(); renderCka();
    msg.textContent = okText; msg.className = "small ok-text";
  } catch (e) { msg.textContent = e.message; msg.className = "small bad-text"; }
}
$("btnCkaNew").onclick = () => {
  if (view && view.zapas && !confirm("Založit nové utkání z odkazu? Otevřené utkání zůstane uložené v seznamu.")) return;
  ckaCall("/api/zapas/z-odkazu", { odkaz: $("ckaUrl").value }, "Utkání založeno z výsledkového servisu ✓");
};
$("btnCkaLink").onclick = () => ckaCall("/api/zapas/zdroj", { odkaz: $("ckaUrl").value, automaticky: true },
  "Propojeno – výsledky se načítají ✓");
$("ckaAuto").onchange = (e) => ckaCall("/api/zapas/zdroj", { odkaz: view.zapas.odkazVysledky, automaticky: e.target.checked },
  e.target.checked ? "Automatické načítání zapnuto ✓" : "Automatické načítání vypnuto");
$("btnCkaNow").onclick = () => ckaCall("/api/zapas/nacist", {}, "Načteno ✓");
$("btnCkaUnlink").onclick = () => {
  if (confirm("Zrušit propojení s výsledkovým servisem? Výsledky zůstanou, dál se zapisují ručně."))
    ckaCall("/api/zapas/zdroj", { odkaz: "", automaticky: false }, "Propojení zrušeno");
};

// A match read from the web changes by itself: refresh the scoring page (never while typing).
let lastViewJSON = "";
setInterval(async () => {
  const onScoring = $("tab-zapis").classList.contains("active"), onSetup = $("tab-utkani").classList.contains("active");
  if (!view || !view.zapas || !view.zapas.nacitatVysledky || !(onScoring || onSetup)) return;
  const a = document.activeElement;
  if (a && (a.tagName === "INPUT" || a.tagName === "SELECT") && !a.closest("#ckaCard")) return;
  if ($("plDlg").open) return;
  let v;
  try { v = await api("GET", "/api/zapas"); } catch (_) { return; }
  const j = JSON.stringify(v);
  if (j === lastViewJSON) return;
  lastViewJSON = j;
  view = v;
  if (onScoring) renderScoring(); else renderCka();
}, 5000);

function partDuels(p) {
  const n = view.pravidla.soubojuNaCast, out = [];
  for (let i = (p - 1) * n; i < p * n && i < view.zapas.souboje.length; i++) out.push(i);
  return out;
}
function partDone(p) { return partDuels(p).every((i) => view.vysledek.souboje[i].dohrano); }

// Physical lane of duel di, side "d"/"h", segment j from the rules' lane plan (0 = not set).
function laneOf(di, side, j) {
  const plan = view.pravidla.poradiDrah || [], n = view.pravidla.soubojuNaCast || 1;
  const sl = plan[di % n];
  const seq = sl ? (side === "d" ? sl.domaci : sl.hoste) : null;
  return seq && seq[j] ? seq[j] : 0;
}

function renderDuels() {
  const m = view.zapas, r = view.vysledek, lanes = view.pravidla.drah;
  const box = $("zDuels");
  const focused = document.activeElement && box.contains(document.activeElement) ? document.activeElement.dataset.key : null;
  const duels = partDuels(m.aktualniCast);
  box.replaceChildren();
  duels.forEach((di, k) => {
    const d = m.souboje[di], dr = r.souboje[di];
    const card = document.createElement("div");
    card.className = "card duel";
    const det = !!view.pravidla.podrobnyZapis;
    let head = `<th class="name">Souboj ${di + 1}</th>`;
    for (let j = 0; j < lanes; j++) head += `<th>${j + 1}. úsek${det ? `<br><span class="muted small">plné · dor. · ch.</span>` : ""}</th>`;
    head += `<th>Průběžně</th><th>Součet</th><th>SB</th><th>Body</th>`;
    const row = (side) => {
      const home = side === "d";
      const absent = !!d[side + "Nenastoupil"], sub = d[side + "Stridani"];
      const name = esc(home ? d.domaci : d.hoste) || (home ? "domácí" : "hosté");
      const subTxt = absent ? `<span class="sub">nenastoupil</span>`
        : sub ? `<span class="sub">⇄ ${esc(sub.jmeno)} od ${sub.odHodu}. hodu</span>` : "";
      let h = `<td class="name${absent ? " absent" : ""}"><button type="button" class="pl" data-duel="${di}" data-side="${side}"
        title="Střídání / nenastoupil">${name}</button>${subTxt}</td>`;
      const dis = absent ? " disabled" : "";
      for (let j = 0; j < lanes; j++) {
        const v = d.useky[j][side];
        // Tab/Enter order: lane by lane, all players of the part (as the scores come in).
        const order = j * duels.length * 2 + k * 2 + (home ? 0 : 1);
        const lane = laneOf(di, side, j);
        if (det) {
          const dd = d.useky[j][side + "Podrobne"] || {};
          const f = (fld, label, cls) => `<input class="pin ${cls}" inputmode="numeric" data-key="${di}:${j}:${side}:${fld}"
            data-order="${order * 3 + ["p", "d", "ch"].indexOf(fld)}" placeholder="${label}" title="${label}${lane ? ` – dráha ${lane}` : ""}"${dis}
            value="${dd[fld] ?? ""}">`;
          h += `<td><div class="det">${f("p", "plné", "")}${f("d", "dor.", "")}${f("ch", "ch.", "ch")}</div>` +
            `<span class="detsum">${v ?? (lane ? "dr. " + lane : "")}</span></td>`;
          continue;
        }
        const hint = lane ? ` placeholder="dr. ${lane}" title="${j + 1}. úsek na dráze ${lane}"` : "";
        h += `<td><input class="pin" inputmode="numeric" data-key="${di}:${j}:${side}" data-order="${order}"${hint}${dis} value="${v ?? ""}"></td>`;
      }
      const lv = d.prubezne[side];
      const nowLane = laneOf(di, side, d.useky.findIndex((p) => p[side] == null)); // lane being played now
      const liveHint = nowLane ? ` placeholder="dr. ${nowLane}" title="právě hraje na dráze ${nowLane}"` : "";
      h += `<td><input class="pin live" inputmode="numeric" data-key="${di}:-1:${side}" data-order="${100000 + k * 2 + (home ? 0 : 1)}"${liveHint}${dis} value="${lv ?? ""}"></td>`;
      const sum = home ? dr.dSoucet : dr.hSoucet, sb = home ? dr.dSB : dr.hSB, p = home ? dr.dBody : dr.hBody;
      const win = dr.dohrano && p > (home ? dr.hBody : dr.dBody);
      h += `<td class="sum">${sum}</td><td class="sb">${pts(sb)}</td><td class="pts${win ? " win" : ""}">${dr.dohrano ? pts(p) : "–"}</td>`;
      return `<tr>${h}</tr>`;
    };
    card.innerHTML = `<table><thead><tr>${head}</tr></thead><tbody>${row("d")}${row("h")}</tbody></table>`;
    box.append(card);
  });
  box.querySelectorAll("button.pl").forEach((b) => { b.onclick = () => openPlayer(Number(b.dataset.duel), b.dataset.side); });
  box.querySelectorAll("input.pin").forEach((inp) => {
    markWarn(inp);
    inp.addEventListener("change", () => saveCell(inp));
    inp.addEventListener("input", () => markWarn(inp));
    inp.addEventListener("keydown", (e) => {
      if (e.key === "Enter") { e.preventDefault(); saveCell(inp); focusNext(inp); }
    });
    inp.addEventListener("focus", () => inp.select());
  });
  if (focused) { const el = box.querySelector(`[data-key="${focused}"]`); if (el) el.focus(); }
}

function markWarn(inp) {
  const v = inp.value.trim();
  const n = Number(v);
  const max = inp.dataset.key.endsWith(":ch") ? (view.pravidla.hoduNaUsek || 30) : view.pravidla.maxKuzelekNaDrahu;
  inp.classList.toggle("err", v !== "" && (!/^\d+$/.test(v) || n > max));
  inp.classList.toggle("warn", /^\d+$/.test(v) && n > 200 && n <= view.pravidla.maxKuzelekNaDrahu);
}

function focusNext(inp) {
  const all = [...$("zDuels").querySelectorAll("input.pin")].sort((a, b) => a.dataset.order - b.dataset.order);
  const i = all.indexOf(inp);
  if (i >= 0 && i + 1 < all.length) all[i + 1].focus();
}

async function saveCell(inp) {
  const [duel, lane, side, field] = inp.dataset.key.split(":");
  const raw = inp.value.trim();
  if (raw !== "" && !/^\d+$/.test(raw)) { inp.classList.add("err"); return; }
  const value = raw === "" ? null : Number(raw);
  const cur = (() => {
    const d = view.zapas.souboje[duel];
    if (Number(lane) === -1) return d.prubezne[side];
    if (field) return (d.useky[lane][side + "Podrobne"] || {})[field];
    return d.useky[lane][side];
  })();
  if ((cur ?? null) === value) return; // unchanged
  await send("/api/zapas/hodnota", { souboj: Number(duel), usek: Number(lane), strana: side, hodnota: value, pole: field || "" });
}

async function send(url, body) {
  try { applyView(await api("POST", url, body)); showMsg(""); }
  catch (e) { showMsg(e.message); }
}

// ---- player dialog: substitution (SŘ čl. 12) or an empty place (čl. 15)
let plTarget = null;
function openPlayer(di, side) {
  const d = view.zapas.souboje[di], r = view.vysledek.souboje[di];
  const f = $("plForm").elements;
  plTarget = { di, side };
  const name = (side === "d" ? d.domaci : d.hoste) || "hráč";
  const team = side === "d" ? view.zapas.domaci.nazev : view.zapas.hoste.nazev;
  $("plTitle").textContent = `${name} (${team}, souboj ${di + 1})`;
  const sub = d[side + "Stridani"];
  // Default "from throw": the first throw of the first segment without a result.
  const done = d.useky.filter((p) => p[side] != null).length;
  f["jmeno"].value = sub ? sub.jmeno : "";
  f["odHodu"].value = sub ? sub.odHodu : done * (view.pravidla.hoduNaUsek || 30) + 1;
  f["odHodu"].max = view.pravidla.drah * (view.pravidla.hoduNaUsek || 30);
  f["nenastoupil"].checked = !!d[side + "Nenastoupil"];
  $("plClear").hidden = !sub;
  $("plMsg").textContent = "";
  // Offer the team's other players from its saved roster.
  const roster = (typeof rosters !== "undefined" ? rosters : []).find((x) => x.nazev === team);
  const used = new Set(view.zapas.souboje.map((x) => side === "d" ? x.domaci : x.hoste));
  $("plNames").replaceChildren(...((roster && roster.hraci) || []).filter((p) => p && !used.has(p)).map((p) => new Option(p)));
  $("plDlg").showModal();
}
$("plForm").addEventListener("submit", async (e) => {
  const action = e.submitter ? e.submitter.value : "save";
  if (action === "cancel" || !plTarget) return;
  e.preventDefault();
  const f = $("plForm").elements;
  const body = { souboj: plTarget.di, strana: plTarget.side, nenastoupil: f["nenastoupil"].checked, stridani: null };
  if (action === "save" && f["jmeno"].value.trim() && !body.nenastoupil) {
    body.stridani = { jmeno: f["jmeno"].value.trim(), odHodu: Number(f["odHodu"].value) || 1 };
  }
  try {
    applyView(await api("POST", "/api/zapas/hrac", body));
    $("plDlg").close();
  } catch (err) { $("plMsg").textContent = err.message; }
});

$("btnUndo").onclick = () => send("/api/zapas/zpet", {});
document.addEventListener("keydown", (e) => {
  if ((e.ctrlKey || e.metaKey) && e.key === "z" && !$("tab-zapis").hidden && $("tab-zapis").classList.contains("active")) {
    e.preventDefault();
    send("/api/zapas/zpet", {});
  }
});
document.querySelectorAll("#zModes button").forEach((b) => {
  b.onclick = () => send("/api/zapas/grafika", { rezim: b.dataset.mode, zprava: $("zMessage").value });
});
$("btnMsg").onclick = () => send("/api/zapas/grafika", { rezim: "zprava", zprava: $("zMessage").value });
$("zMessage").addEventListener("keydown", (e) => { if (e.key === "Enter") $("btnMsg").click(); });

// Graphics preview: the transparent overlay on a checkerboard (works without streaming).
const gfxLoader = new Image();
gfxLoader.onload = () => { $("gfxPreview").src = gfxLoader.src; };
window.refreshGfxPreview = function () {
  if ($("tab-zapis").classList.contains("active")) gfxLoader.src = "/api/grafika.png?t=" + Date.now();
};

// ================================================================== Utkání (setup)

const mform = $("matchForm");
let rosters = [];

function renderForm() {
  const m = view && view.zapas;
  const n = view ? view.pravidla.hracuNaTym : 6;
  const per = view ? view.pravidla.soubojuNaCast : 2;
  $("mfTitle").textContent = m ? `Aktuální utkání: ${m.domaci.nazev} – ${m.hoste.nazev}` : "Nové utkání";
  $("btnMatchSave").textContent = m ? "Uložit změny" : "Založit utkání";
  $("btnMatchNew").hidden = !m;
  $("btnMatchClose").hidden = !m;
  const tb = $("mfPlayers");
  tb.replaceChildren();
  for (let i = 0; i < n; i++) {
    const tr = document.createElement("tr");
    tr.innerHTML = `<td>${i + 1}.</td><td>${Math.floor(i / per) + 1}.</td>
      <td><input name="d${i}"></td><td><input name="h${i}"></td>`;
    tb.append(tr);
  }
  const f = mform.elements;
  const today = new Date().toISOString().slice(0, 10);
  f["datum"].value = m ? m.datum : today;
  f["soutez"].value = m ? m.soutez : "1. KLM";
  f["kolo"].value = m ? m.kolo : "";
  f["misto"].value = m ? m.misto : "";
  f["domaci.nazev"].value = m ? m.domaci.nazev : "";
  f["domaci.zkratka"].value = m ? m.domaci.zkratka : "";
  f["hoste.nazev"].value = m ? m.hoste.nazev : "";
  f["hoste.zkratka"].value = m ? m.hoste.zkratka : "";
  for (let i = 0; i < n; i++) {
    f["d" + i].value = m ? m.souboje[i].domaci : "";
    f["h" + i].value = m ? m.souboje[i].hoste : "";
  }
  $("mfMsg").textContent = "";
}

function formToMatch() {
  const f = mform.elements, n = view ? view.pravidla.hracuNaTym : 6;
  const m = {
    datum: f["datum"].value, soutez: f["soutez"].value.trim(), kolo: f["kolo"].value.trim(), misto: f["misto"].value.trim(),
    domaci: { nazev: f["domaci.nazev"].value.trim(), zkratka: f["domaci.zkratka"].value.trim() },
    hoste: { nazev: f["hoste.nazev"].value.trim(), zkratka: f["hoste.zkratka"].value.trim() },
    souboje: [],
  };
  for (let i = 0; i < n; i++) m.souboje.push({ domaci: f["d" + i].value.trim(), hoste: f["h" + i].value.trim() });
  return m;
}

async function loadRosters() {
  try { rosters = (await api("GET", "/api/soupisky")) || []; } catch (_) { rosters = []; }
  $("teamList").replaceChildren(...rosters.map((r) => new Option(r.nazev)));
  const tb = $("rosterList");
  tb.replaceChildren();
  if (!rosters.length) { tb.innerHTML = `<tr><td class="muted">Zatím žádné.</td></tr>`; return; }
  for (const r of rosters) {
    const tr = document.createElement("tr");
    const players = (r.hraci || []).filter(Boolean);
    tr.innerHTML = `<td><b>${esc(r.nazev)}</b></td><td class="muted small">${esc(players.join(", "))}</td><td></td>`;
    const del = document.createElement("button");
    del.type = "button"; del.className = "del"; del.textContent = "Smazat";
    del.onclick = async () => {
      if (!confirm(`Smazat tým „${r.nazev}“ a jeho soupisku?\n\nSmaže se na tomto počítači i na Google Disku. Uložená utkání zůstanou.`)) return;
      try { await api("POST", "/api/soupisky/smazat", { soubor: r.soubor }); loadRosters(); }
      catch (err) { $("mfMsg").textContent = err.message; }
    };
    tr.lastElementChild.append(del);
    tb.append(tr);
  }
}

// Picking a known team fills its short name and players (only into empty fields).
["domaci", "hoste"].forEach((side, s) => {
  mform.elements[side + ".nazev"].addEventListener("change", (e) => {
    const r = rosters.find((x) => x.nazev === e.target.value);
    if (!r) return;
    const f = mform.elements;
    if (!f[side + ".zkratka"].value) f[side + ".zkratka"].value = r.zkratka || "";
    (r.hraci || []).forEach((p, i) => {
      const inp = f[(s === 0 ? "d" : "h") + i];
      if (inp && !inp.value) inp.value = p;
    });
  });
});

mform.addEventListener("submit", async (e) => {
  e.preventDefault();
  const msg = $("mfMsg");
  const isNew = !(view && view.zapas);
  try {
    view = await api(isNew ? "POST" : "PUT", isNew ? "/api/zapas/novy" : "/api/zapas/nastaveni", formToMatch());
    renderForm();
    msg.textContent = isNew ? "Utkání založeno ✓ – pokračujte na záložce Zápis" : "Uloženo ✓";
    msg.className = "small ok-text";
    loadSaved(); loadRosters();
  } catch (err) { msg.textContent = err.message; msg.className = "small bad-text"; }
});

$("btnMatchNew").onclick = async () => {
  if (!confirm("Založit nové utkání s týmy a hráči z formuláře?\n\nAktuální utkání zůstane uložené v seznamu.")) return;
  const msg = $("mfMsg");
  try {
    view = await api("POST", "/api/zapas/novy", formToMatch());
    renderForm(); loadSaved();
    msg.textContent = "Nové utkání založeno ✓"; msg.className = "small ok-text";
  } catch (err) { msg.textContent = err.message; msg.className = "small bad-text"; }
};
$("btnMatchClose").onclick = async () => {
  if (!confirm("Zavřít utkání? Zůstane uložené a půjde znovu otevřít. Grafika přestane ukazovat skóre.")) return;
  view = await api("POST", "/api/zapas/zavrit", {});
  renderForm();
};

async function loadSaved() {
  let list = [];
  try { list = (await api("GET", "/api/zapasy")) || []; } catch (_) { /* ignore */ }
  const tb = $("savedList");
  tb.replaceChildren();
  if (!list.length) { tb.innerHTML = `<tr><td class="muted">Zatím žádná.</td></tr>`; return; }
  for (const s of list) {
    const tr = document.createElement("tr");
    const current = view && s.soubor === view.soubor;
    tr.innerHTML = `<td>${esc(s.datum)}</td><td><b>${esc(s.domaci)}</b> – <b>${esc(s.hoste)}</b></td>
      <td class="muted small">změněno ${esc(s.zmeneno)}</td><td></td>`;
    const b = document.createElement("button");
    b.textContent = current ? "Otevřené" : "Otevřít";
    b.disabled = current;
    b.onclick = async () => {
      try { view = await api("POST", "/api/zapasy/otevrit", { soubor: s.soubor }); renderForm(); loadSaved(); }
      catch (err) { $("mfMsg").textContent = err.message; }
    };
    const del = document.createElement("button");
    del.type = "button"; del.className = "del"; del.textContent = "Smazat";
    del.onclick = async () => {
      const what = `${s.datum} ${s.domaci} – ${s.hoste}`;
      const openNote = current ? "Utkání je právě otevřené – zavře se a grafika přestane ukazovat skóre.\n\n" : "";
      if (!confirm(`Smazat utkání ${what}?\n\n${openNote}` +
        "Smaže se na tomto počítači, na Google Disku (do koše) i z tabulky výsledků.")) return;
      try {
        view = await api("POST", "/api/zapasy/smazat", { soubor: s.soubor });
        renderForm(); loadSaved();
      } catch (err) { $("mfMsg").textContent = err.message; }
    };
    tr.lastElementChild.append(b, del);
    tb.append(tr);
  }
}

// Open the tab from the address (#zapis / #utkani) once this script is loaded.
if (location.hash === "#zapis" || location.hash === "#utkani") window.loadMatch(location.hash.slice(1));
