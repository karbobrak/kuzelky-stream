"use strict";
// Tab "Obraz a grafika": crop editor (camera → 16:9) and drag-and-drop layout of the graphics.
(function () {
  let cfg = null;      // settings from the server
  let pic = null;      // working copy of cfg.obraz (crop)
  let layout = null;   // working copy of cfg.grafika.rozlozeni
  const snap = $("snap");
  let snapOK = false;
  let rotated = null;  // offscreen canvas: snapshot with rotation/mirror applied

  const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));
  const r1 = (v) => Math.round(v * 10) / 10;

  window.loadEditor = async function () {
    try {
      const v = await api("GET", "/api/nastaveni");
      cfg = v.nastaveni;
    } catch (e) { $("cropMsg").textContent = e.message; return; }
    pic = { ...cfg.obraz };
    layout = structuredClone(cfg.grafika.rozlozeni);
    syncCropControls();
    syncLayoutControls();
    fillPresets();
    loadSnap(false);
    loadBlocks();
  };

  // ================================================================ crop editor

  function loadSnap(fresh) {
    snapOK = false;
    $("snapMsg").textContent = "Načítám snímek z kamery…";
    $("snapMsg").hidden = false;
    $("cropRect").hidden = true;
    snap.src = "/api/kamera/snimek.jpg?" + (fresh ? "novy=1&" : "") + "t=" + Date.now();
  }
  snap.onload = () => {
    snapOK = true;
    rotated = null;
    $("snapMsg").hidden = true;
    $("cropRect").hidden = false;
    redrawCrop();
  };
  snap.onerror = () => {
    $("snapMsg").textContent = "Snímek z kamery se nepodařilo načíst. Zkontrolujte kameru (Nastavení → Otestovat kameru) a zkuste Nový snímek.";
    $("snapMsg").hidden = false;
    // Keep the stage a sensible size without an image.
    snap.removeAttribute("src");
    snap.style.aspectRatio = "16 / 9";
  };
  $("btnSnap").onclick = () => loadSnap(true);

  function syncCropControls() {
    $("cropLock").checked = pic.zamek169;
    $("cropRot").checked = pic.otoceni180;
    $("cropMirror").checked = pic.zrcadlit;
    $("cropFit").value = pic.prizpusobeni;
    $("cropX").value = r1(pic.vyrezX); $("cropY").value = r1(pic.vyrezY);
    $("cropW").value = r1(pic.vyrezSirka); $("cropH").value = r1(pic.vyrezVyska);
  }

  // Ratio of crop height% to width% that gives a 16:9 picture on this camera.
  function lockRatio() {
    const out = cfg.vystup.sirka / cfg.vystup.vyska;
    return snapOK ? (snap.naturalWidth / snap.naturalHeight) / out : 1;
  }

  function setCrop(x, y, w, h) {
    w = clamp(w, 5, 100); h = clamp(h, 5, 100);
    x = clamp(x, 0, 100 - w); y = clamp(y, 0, 100 - h);
    pic.vyrezX = x; pic.vyrezY = y; pic.vyrezSirka = w; pic.vyrezVyska = h;
    syncCropControls();
    redrawCrop();
  }

  function redrawCrop() {
    const rc = $("cropRect");
    rc.style.left = pic.vyrezX + "%"; rc.style.top = pic.vyrezY + "%";
    rc.style.width = pic.vyrezSirka + "%"; rc.style.height = pic.vyrezVyska + "%";
    const sx = (pic.otoceni180 ? -1 : 1) * (pic.zrcadlit ? -1 : 1), sy = pic.otoceni180 ? -1 : 1;
    snap.style.transform = `scale(${sx}, ${sy})`;
    drawOutput($("cropResult"));
    drawOutput($("layoutBg"));
    if (snapOK) {
      const w = Math.round(snap.naturalWidth * pic.vyrezSirka / 100), h = Math.round(snap.naturalHeight * pic.vyrezVyska / 100);
      const ow = cfg.vystup.sirka, oh = cfg.vystup.vyska;
      const soft = w < ow || h < oh;
      $("cropInfo").textContent = `Výřez ${w} × ${h} px z ${snap.naturalWidth} × ${snap.naturalHeight}, vysílá se ${ow} × ${oh}.` +
        (soft ? " Výřez je menší než výstup – obraz bude méně ostrý (digitální zoom)." : "");
      $("cropInfo").className = "small " + (soft ? "bad-text" : "muted");
    }
  }

  // The snapshot with rotation/mirror applied, as the crop coordinates see it.
  function rotatedSource() {
    if (rotated && rotated.dataset.key === `${pic.otoceni180}${pic.zrcadlit}`) return rotated;
    const c = document.createElement("canvas");
    c.width = snap.naturalWidth; c.height = snap.naturalHeight;
    const ctx = c.getContext("2d");
    const sx = (pic.otoceni180 ? -1 : 1) * (pic.zrcadlit ? -1 : 1), sy = pic.otoceni180 ? -1 : 1;
    ctx.translate(sx < 0 ? c.width : 0, sy < 0 ? c.height : 0);
    ctx.scale(sx, sy);
    ctx.drawImage(snap, 0, 0);
    c.dataset.key = `${pic.otoceni180}${pic.zrcadlit}`;
    rotated = c;
    return c;
  }

  // drawOutput renders what viewers will see (crop + fit) into a 16:9 canvas.
  function drawOutput(canvas) {
    const ctx = canvas.getContext("2d");
    const cw = canvas.width, ch = canvas.height;
    ctx.fillStyle = "#000";
    ctx.fillRect(0, 0, cw, ch);
    if (!snapOK || !pic) return;
    const src = rotatedSource();
    let sx = src.width * pic.vyrezX / 100, sy = src.height * pic.vyrezY / 100;
    let sw = src.width * pic.vyrezSirka / 100, sh = src.height * pic.vyrezVyska / 100;
    const aspect = cw / ch;
    if (pic.prizpusobeni === "pruhy") {
      const k = Math.min(cw / sw, ch / sh), dw = sw * k, dh = sh * k;
      ctx.drawImage(src, sx, sy, sw, sh, (cw - dw) / 2, (ch - dh) / 2, dw, dh);
      return;
    }
    if (pic.prizpusobeni === "orez") {
      if (sw / sh > aspect) { const nw = sh * aspect; sx += (sw - nw) / 2; sw = nw; }
      else { const nh = sw / aspect; sy += (sh - nh) / 2; sh = nh; }
    }
    ctx.drawImage(src, sx, sy, sw, sh, 0, 0, cw, ch);
  }

  // Dragging the crop rectangle (move) or a corner handle (resize).
  (function () {
    const stage = $("cropStage"), rc = $("cropRect");
    let drag = null;
    rc.addEventListener("pointerdown", (e) => {
      if (!snapOK) return;
      e.preventDefault();
      rc.setPointerCapture(e.pointerId);
      const corner = e.target.classList.contains("h") ? [...e.target.classList].find((c) => c.length === 2) : null;
      drag = { corner, x0: e.clientX, y0: e.clientY, p: { ...pic } };
    });
    rc.addEventListener("pointermove", (e) => {
      if (!drag) return;
      const b = stage.getBoundingClientRect();
      const dx = (e.clientX - drag.x0) / b.width * 100, dy = (e.clientY - drag.y0) / b.height * 100;
      const p = drag.p;
      if (!drag.corner) { setCrop(p.vyrezX + dx, p.vyrezY + dy, p.vyrezSirka, p.vyrezVyska); return; }
      const left = drag.corner.includes("w"), top = drag.corner.includes("n");
      // Anchor = the opposite corner, which stays in place.
      const ax = left ? p.vyrezX + p.vyrezSirka : p.vyrezX;
      const ay = top ? p.vyrezY + p.vyrezVyska : p.vyrezY;
      let w = clamp(p.vyrezSirka + (left ? -dx : dx), 5, left ? ax : 100 - ax);
      let h = clamp(p.vyrezVyska + (top ? -dy : dy), 5, top ? ay : 100 - ay);
      if (pic.zamek169) {
        const k = lockRatio();
        h = w * k;
        const maxH = top ? ay : 100 - ay;
        if (h > maxH) { h = maxH; w = h / k; }
      }
      setCrop(left ? ax - w : ax, top ? ay - h : ay, w, h);
    });
    const end = () => { drag = null; };
    rc.addEventListener("pointerup", end);
    rc.addEventListener("pointercancel", end);
  })();

  ["cropX", "cropY", "cropW", "cropH"].forEach((id) => {
    $(id).addEventListener("change", () => {
      let w = Number($("cropW").value), h = Number($("cropH").value);
      if (pic.zamek169 && id === "cropW") h = w * lockRatio();
      if (pic.zamek169 && id === "cropH") w = h / lockRatio();
      setCrop(Number($("cropX").value), Number($("cropY").value), w, h);
    });
  });
  $("cropLock").onchange = (e) => {
    pic.zamek169 = e.target.checked;
    if (pic.zamek169) setCrop(pic.vyrezX, pic.vyrezY, pic.vyrezSirka, pic.vyrezSirka * lockRatio());
  };
  $("cropRot").onchange = (e) => { pic.otoceni180 = e.target.checked; redrawCrop(); };
  $("cropMirror").onchange = (e) => { pic.zrcadlit = e.target.checked; redrawCrop(); };
  $("cropFit").onchange = (e) => { pic.prizpusobeni = e.target.value; redrawCrop(); };
  $("btnCropFull").onclick = () => {
    if (pic.zamek169) {
      // Largest 16:9 crop centred on the frame.
      const k = lockRatio();
      let w = 100, h = 100 * k;
      if (h > 100) { h = 100; w = 100 / k; }
      setCrop((100 - w) / 2, (100 - h) / 2, w, h);
    } else setCrop(0, 0, 100, 100);
  };
  $("btnCropRevert").onclick = () => { pic = { ...cfg.obraz }; syncCropControls(); redrawCrop(); $("cropMsg").textContent = ""; };

  $("btnCropApply").onclick = async () => {
    const msg = $("cropMsg");
    const c = structuredClone(cfg);
    c.obraz = { ...pic };
    try {
      const check = await api("POST", "/api/nastaveni/kontrola", c);
      if (check.dopad !== "zadny" &&
        !confirm("Vysílání běží. Kamera se znovu připojí a divák uvidí na pár sekund náhradní obraz.\n\nPoužít výřez teď?")) return;
      await api("PUT", "/api/nastaveni", c);
      cfg.obraz = { ...pic };
      msg.textContent = "Použito ✓"; msg.className = "small ok-text";
    } catch (e) { msg.textContent = e.message; msg.className = "small bad-text"; }
  };

  // ================================================================ layout editor

  const blocks = { lista: $("blkBar"), zprava: $("blkMsg") };
  const placeOf = (blk) => (blk === "lista" ? layout.lista : layout.zprava);

  function loadBlocks(only) {
    for (const [name, img] of Object.entries(blocks)) {
      if (only && only !== name) continue;
      const pre = new Image();
      pre.onload = () => { img.src = pre.src; img.dataset.w = pre.naturalWidth; img.dataset.h = pre.naturalHeight; positionBlock(name); };
      pre.src = `/api/grafika/blok.png?blok=${name}&t=${Date.now()}`;
    }
  }

  // Block size in percent of the picture.
  function blockSize(name) {
    const img = blocks[name];
    return { w: img.dataset.w / cfg.vystup.sirka * 100, h: img.dataset.h / cfg.vystup.vyska * 100 };
  }

  function positionBlock(name) {
    const img = blocks[name], p = placeOf(name);
    if (!img.dataset.w) return;
    const s = blockSize(name);
    const left = clamp(p.x - s.w / 2, 0, 100 - s.w);
    const top = name === "lista" ? clamp(p.y - s.h, 0, 100 - s.h) : clamp(p.y, 0, 100 - s.h);
    Object.assign(img.style, { left: left + "%", top: top + "%", width: s.w + "%" });
  }

  function syncLayoutControls() {
    $("scaleBar").value = layout.lista.meritko; $("scaleBarV").textContent = layout.lista.meritko + " %";
    $("scaleMsg").value = layout.zprava.meritko; $("scaleMsgV").textContent = layout.zprava.meritko + " %";
    $("opacity").value = layout.pruhlednostPozadi; $("opacityV").textContent = layout.pruhlednostPozadi + " %";
  }

  // Send the layout to the app (live in the stream). Throttled: one request in flight, the latest wins.
  let sending = false, pending = false;
  async function sendLayout(reload) {
    if (sending) { pending = true; return; }
    sending = true;
    try { await api("POST", "/api/grafika/rozlozeni", layout); $("layoutMsg").textContent = ""; }
    catch (e) { $("layoutMsg").textContent = e.message; $("layoutMsg").className = "small bad-text"; }
    sending = false;
    if (reload) loadBlocks(reload === true ? undefined : reload);
    if (pending) { pending = false; sendLayout(); }
  }

  for (const [name, img] of Object.entries(blocks)) {
    let drag = null;
    img.addEventListener("pointerdown", (e) => {
      e.preventDefault();
      img.setPointerCapture(e.pointerId);
      img.classList.add("dragging");
      const p = placeOf(name);
      drag = { x0: e.clientX, y0: e.clientY, px: p.x, py: p.y };
    });
    img.addEventListener("pointermove", (e) => {
      if (!drag) return;
      const b = $("layoutStage").getBoundingClientRect(), s = blockSize(name), p = placeOf(name);
      let x = drag.px + (e.clientX - drag.x0) / b.width * 100;
      let y = drag.py + (e.clientY - drag.y0) / b.height * 100;
      const snapped = Math.abs(x - 50) < 1.5;
      if (snapped) x = 50;
      $("guideV").hidden = !snapped;
      p.x = r1(clamp(x, s.w / 2, 100 - s.w / 2));
      p.y = r1(name === "lista" ? clamp(y, s.h, 100) : clamp(y, 0, 100 - s.h));
      positionBlock(name);
      sendLayout();
    });
    const end = () => { if (drag) { drag = null; img.classList.remove("dragging"); $("guideV").hidden = true; sendLayout(); } };
    img.addEventListener("pointerup", end);
    img.addEventListener("pointercancel", end);
    img.addEventListener("wheel", (e) => {
      e.preventDefault();
      const p = placeOf(name);
      p.meritko = clamp(p.meritko + (e.deltaY < 0 ? 5 : -5), 30, 200);
      syncLayoutControls();
      sendLayout(name);
    }, { passive: false });
  }

  let sliderTimer = null;
  const onSlider = (fn, reload) => (e) => {
    fn(Number(e.target.value));
    syncLayoutControls();
    clearTimeout(sliderTimer);
    sliderTimer = setTimeout(() => sendLayout(reload), 120);
  };
  $("scaleBar").oninput = onSlider((v) => (layout.lista.meritko = v), "lista");
  $("scaleMsg").oninput = onSlider((v) => (layout.zprava.meritko = v), "zprava");
  $("opacity").oninput = onSlider((v) => (layout.pruhlednostPozadi = v), true);

  // Presets.
  function fillPresets(selected) {
    const sel = $("presetSel");
    sel.replaceChildren(new Option("— předvolby —", ""), ...Object.keys(cfg.grafika.predvolby || {}).sort().map((n) => new Option(n, n)));
    if (selected) sel.value = selected;
  }
  async function preset(action, name) {
    const msg = $("layoutMsg");
    try {
      const r = await api("POST", "/api/grafika/predvolba", { akce: action, nazev: name });
      cfg.grafika.predvolby = r.predvolby || {};
      layout = structuredClone(r.rozlozeni);
      syncLayoutControls(); fillPresets(action === "smazat" ? "" : name); loadBlocks();
      msg.textContent = { ulozit: "Předvolba uložena ✓", nacist: "Načteno ✓", smazat: "Smazáno ✓", vychozi: "Výchozí rozmístění ✓" }[action];
      msg.className = "small ok-text";
    } catch (e) { msg.textContent = e.message; msg.className = "small bad-text"; }
  }
  $("btnLayoutDefault").onclick = () => preset("vychozi", "");
  $("btnPresetLoad").onclick = () => $("presetSel").value && preset("nacist", $("presetSel").value);
  $("btnPresetDel").onclick = () => {
    const n = $("presetSel").value;
    if (n && confirm(`Smazat předvolbu „${n}“?`)) preset("smazat", n);
  };
  $("btnPresetSave").onclick = () => preset("ulozit", $("presetName").value.trim());

  window.addEventListener("resize", () => { if (layout) { positionBlock("lista"); positionBlock("zprava"); } });

  if (location.hash === "#obraz") window.loadEditor();
})();
