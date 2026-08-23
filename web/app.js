(() => {
  const $ = (id) => document.getElementById(id);

  const state = {
    meta: null,
    sweep: null,
    p: 0,
    selected: 0,
    enabled: new Set(),
    bundle: null,
    extraScenarios: [],
    // inflight is the sweep currently being poured. Every control re-sweeps, so
    // dragging the agent picker used to leave several requests racing and the
    // last reply to land won, which is not the same as the last one asked for.
    inflight: null,
    pending: false,
  };

  // num reads a numeric input without treating 0 as "unset". `Number(v) || d`
  // silently rewrote seed 0 to 42, so the one seed a user is most likely to
  // type first was the one seed they could not run.
  function num(id, fallback) {
    const raw = ($(id) && $(id).value || "").trim();
    if (raw === "") return fallback;
    const n = Number(raw);
    return Number.isFinite(n) ? n : fallback;
  }

  function seedValue() {
    return num("seed", 42);
  }

  function trialCount() {
    const n = num("trials", 40);
    return n > 0 ? n : 40;
  }

  const heatWord = (p) => {
    if (p <= 0) return "cold";
    if (p < 8) return "warm";
    if (p < 18) return "forging";
    if (p < 26) return "white-hot";
    return "melt";
  };

  async function loadMeta() {
    state.meta = await getJSON("/api/meta");
    $("agent-name").textContent = state.meta.agent.name;
    $("agent-fw").textContent = state.meta.agent.framework || "langgraph-go";
    const defaults = state.meta.defaults.faults || [];
    state.enabled = new Set(defaults);
    fillSelect($("agent"), state.meta.agents || [], "id", "id", state.meta.defaults.agent);
    fillScenarios();
    $("agent-task").textContent = scenarioLabel($("scenario").value) || "—";
    $("runtime-pill").textContent = runtimeLabel(state.meta.runtime || {});
    $("ai-pill").textContent = "ai: " + ((state.meta.ai && state.meta.ai.provider) || "local");
    renderFaults();
    drawGraph(graphNodes(), new Set(), new Set());
  }

  // runtimeLabel says which sidecars are actually up. The old copy claimed
  // "up · langgraph" whenever any sidecar answered, including when langgraph
  // was the one thing missing.
  function runtimeLabel(rt) {
    const up = [];
    if (rt.ready && rt.langgraph) up.push("langgraph");
    if (rt.ready && rt.adk) up.push("adk");
    if (rt.js) up.push("node");
    return up.length ? "runtime: " + up.join(" + ") : "runtime: go only";
  }

  async function getJSON(url, init) {
    const res = await fetch(url, init);
    if (!res.ok) {
      const body = (await res.text().catch(() => "")).trim();
      throw new Error(`${res.status} ${res.statusText}${body ? ": " + body.slice(0, 300) : ""}`);
    }
    return res.json();
  }

  function graphNodes() {
    const spec = (state.bundle && state.bundle.spec) || (state.meta && state.meta.agent) || {};
    const nodes = (spec.graph && spec.graph.nodes) || [];
    return nodes.length ? nodes : (state.meta && state.meta.agent && state.meta.agent.graph && state.meta.agent.graph.nodes) || [];
  }

  function applyPreset(id, doSweep) {
    const preset = state.meta && state.meta.presets && state.meta.presets[id];
    if (!preset) return false;
    state.bundle = { spec: preset.spec, scenario: preset.scenario || {} };
    $("agent").value = id;
    $("agent-name").textContent = (preset.spec && preset.spec.name) || id;
    $("agent-fw").textContent = (preset.spec && preset.spec.framework) || "langgraph";
    if (preset.scenario && preset.scenario.name) $("agent-task").textContent = preset.scenario.name;
    for (const f of state.meta.faults || []) state.enabled.add(f.type);
    renderFaults();
    fillScenarios();
    if (preset.scenario && (preset.scenario.id || preset.scenario.objective)) {
      $("scenario").value = preset.scenario.id || "pasted";
    }
    if (doSweep !== false) sweep();
    return true;
  }

  function fillSelect(sel, items, valueKey, labelKey, current) {
    sel.innerHTML = "";
    for (const it of items) {
      const opt = document.createElement("option");
      opt.value = it[valueKey];
      opt.textContent = it[labelKey] + (it.framework ? " · " + it.framework : "");
      if (it.available === false && it.runtime === "python") opt.textContent += " (start serve)";
      sel.append(opt);
    }
    if (current) sel.value = current;
  }

  function looksLikeCRM(tools) {
    const names = new Set((tools || []).map((t) => t.name));
    return ["lookup_contact", "get_deal", "write_deal", "send_email", "check_permission"].some((n) =>
      names.has(n)
    );
  }

  function extraItems() {
    return (state.extraScenarios || []).map((d) => ({
      id: d.id,
      name: (d.source && d.source !== "library" ? d.source + ": " : "") + d.name,
      objective: d.objective,
    }));
  }

  function fillScenarios() {
    const custom = state.bundle && !looksLikeCRM(state.bundle.spec && state.bundle.spec.tools);
    const lib = custom ? [] : state.meta.scenarios || [];
    const items = [...lib, ...extraItems()];
    const pasted = state.bundle && state.bundle.scenario;
    if (pasted && (pasted.id || pasted.objective) && !items.find((i) => i.id === (pasted.id || "pasted"))) {
      items.unshift({
        id: pasted.id || "pasted",
        name: pasted.name || "Pasted scenario",
        objective: pasted.objective,
      });
    }
    const current = $("scenario") && $("scenario").value;
    fillSelect($("scenario"), items, "id", "name", current || (items[0] && items[0].id) || state.meta.defaults.scenario);
  }

  function selectedExtra() {
    const id = $("scenario").value;
    return (state.extraScenarios || []).find((s) => s.id === id) || null;
  }

  function scenarioLabel(id) {
    const pasted = state.bundle && state.bundle.scenario;
    if (pasted && (pasted.id || "pasted") === id) return pasted.name || "Pasted scenario";
    const items = [...(state.meta.scenarios || []), ...extraItems()];
    const sc = items.find((s) => s.id === id);
    return sc ? sc.name : "";
  }

  function renderFaults() {
    const row = $("fault-row");
    row.innerHTML = "";
    for (const f of state.meta.faults) {
      const lab = document.createElement("label");
      if (f.mvp) lab.classList.add("mvp");
      const box = document.createElement("input");
      box.type = "checkbox";
      box.checked = state.enabled.has(f.type);
      box.addEventListener("change", () => {
        if (box.checked) state.enabled.add(f.type);
        else state.enabled.delete(f.type);
        lab.classList.toggle("on", box.checked);
        sweep();
      });
      lab.classList.toggle("on", box.checked);
      lab.title = f.blurb;
      lab.append(box, document.createTextNode(f.label));
      row.append(lab);
    }
  }

  function currentSuite() {
    const suites = (state.sweep && state.sweep.suites) || [];
    if (!suites.length) return null;
    const want = Math.round(state.p);
    let best = suites[0];
    let dist = 99;
    for (const s of suites) {
      const sp = Math.round((s.config.p || 0) * 100);
      const d = Math.abs(sp - want);
      if (d < dist) {
        dist = d;
        best = s;
      }
    }
    return best;
  }

  function renderSuite() {
    const suite = currentSuite();
    if (!suite) return;
    // Survival is over the trials that produced a verdict. A suite where the
    // sidecar never started has no verdicts at all, and reading that as 0%
    // survival blames the agent for a missing interpreter.
    const trials = suite.trials || [];
    const scored = suite.scored ?? trials.length;
    const errored = suite.errored || 0;
    const el = $("survival");
    if (scored === 0) {
      el.textContent = "—";
      el.className = "giant";
      $("survival-sub").textContent = suite.error
        ? "no trial ran: " + suite.error
        : "no trial produced a verdict";
    } else {
      const pct = Math.round(suite.survival * 100);
      el.textContent = pct + "%";
      el.className = "giant " + (pct >= 70 ? "" : pct >= 40 ? "hot" : "dead");
      $("survival-sub").textContent =
        `${scored} scored · seed ${suite.config.seed} · p=${Math.round(suite.config.p * 100)}%` +
        (errored ? ` · ${errored} could not run` : "");
    }
    const counts = suite.counts || {};
    const order = ["completed", "recovered", "aborted", "failed"];
    $("counts").innerHTML = order
      .map((k) => `<li><span><i class="dot ${k}"></i> ${k}</span><span>${counts[k] || 0}</span></li>`)
      .join("");

    const tiles = $("tiles");
    // Selecting a tile re-renders the whole grid, which destroys the button the
    // keyboard was on and drops focus to the document. The first Enter worked
    // and every key after it went nowhere -- Space scrolled the page -- so the
    // grid was mouse-only in practice. Put focus back where the user left it.
    const refocus = document.activeElement && document.activeElement.classList.contains("tile");
    tiles.innerHTML = "";
    trials.forEach((t) => {
      const b = document.createElement("button");
      const outcome = t.error ? "errored" : t.outcome;
      b.className = "tile " + outcome + (t.n === state.selected ? " sel" : "");
      const label = `trial ${t.n}: ${outcome}${t.faults?.length ? " · " + t.faults.join("+") : ""}`;
      b.title = label;
      // The tiles have no text, so without a label they are forty buttons a
      // screen reader announces as "button".
      b.setAttribute("aria-label", label);
      b.setAttribute("aria-pressed", String(t.n === state.selected));
      b.addEventListener("click", () => {
        state.selected = t.n;
        renderSuite();
      });
      tiles.append(b);
    });
    if (refocus) {
      const sel = tiles.querySelector(".tile.sel") || tiles.firstElementChild;
      if (sel) sel.focus({ preventScroll: true });
    }
    $("tile-caption").textContent = trials.length ? `trial 0–${trials[trials.length - 1].n}` : "no trials";

    const curve = $("curve");
    if (curve && state.sweep) {
      const want = Math.round(state.p);
      curve.innerHTML = (state.sweep.suites || [])
        .map((s) => {
          const sp = Math.round((s.config.p || 0) * 100);
          const h = Math.max(2, Math.round((s.survival || 0) * 48));
          const on = sp === want ? "on" : "";
          return `<i class="${on}" style="height:${h}px" title="${sp}% → ${Math.round(s.survival * 100)}%"></i>`;
        })
        .join("");
    }

    $("clusters").innerHTML = (suite.clusters || [])
      .slice(0, 8)
      .map((c) => {
        const n = Number(c.sample_trial) || 0;
        return `<li data-sample="${n}"><span>${esc(c.id)}</span><span>${c.n} · ${Math.round(c.rate * 100)}%</span></li>`;
      })
      .join("");
    $("clusters").querySelectorAll("li").forEach((li) => {
      li.addEventListener("click", () => {
        state.selected = Number(li.dataset.sample);
        renderSuite();
      });
    });

    const c = suite.critique || {};
    $("headline").textContent = suite.error && scored === 0 ? "Chamber error: " + suite.error : c.headline || "";
    $("paragraphs").innerHTML = (c.paragraphs || []).map((p) => `<p>${esc(p)}</p>`).join("");
    $("fixes").innerHTML = (c.fixes || [])
      .map((f) => `<li><code>${esc(f.node)}</code> — ${esc(f.advice)}</li>`)
      .join("");

    const trial = trials.find((t) => t.n === state.selected) || trials[0];
    if (trial) renderTrial(suite, trial);

    const heat = Math.min(1, state.p / 30);
    document.documentElement.style.setProperty("--heat", String(0.08 + heat * 0.55));
  }

  function renderTrial(suite, trial) {
    $("tl-title").textContent = `Trial ${trial.n} · ${trial.error ? "chamber error" : trial.outcome}`;
    $("tl-reason").textContent = trial.reason;
    $("replay-cmd").textContent = replayCmd(suite, trial);

    const hit = new Set();
    const faulted = new Set();
    for (const ev of trial.events || []) {
      if (ev.kind === "node_enter" && ev.node) hit.add(ev.node);
      if (ev.kind === "fault") {
        if (ev.node) faulted.add(ev.node);
        if (ev.tool) faulted.add(ev.tool);
      }
    }
    drawGraph(graphNodes(), hit, faulted);

    const tl = $("timeline");
    tl.innerHTML = "";
    for (const ev of trial.events || []) {
      const li = document.createElement("li");
      li.className = String(ev.kind || "").replace(/[^a-z_-]/gi, "");
      li.append(span(String(ev.tick ?? "")), span(String(ev.kind ?? ""), "kind"), span(String(ev.message ?? "")));
      tl.append(li);
    }
  }

  function span(text, cls) {
    const el = document.createElement("span");
    if (cls) el.className = cls;
    el.textContent = text;
    return el;
  }

  // replayCmd has to name the agent, scenario and fault set, not just the seed.
  // A bare `-seed 42 -trial 0` replays the built-in closer against the default
  // scenario, which is a different run from the one on screen.
  function replayCmd(suite, trial) {
    const cfg = suite.config || {};
    const parts = ["crucible", "replay", "-seed", String(cfg.seed ?? 0)];
    parts.push("-trials", String((suite.trials || []).length || cfg.trials || 40));
    parts.push("-trial", String(trial.n), "-p", String(cfg.p ?? 0));
    if (cfg.agent) parts.push("-agent", sh(cfg.agent));
    if (cfg.scenario) parts.push("-scenario", sh(cfg.scenario));
    if ((cfg.faults || []).length) parts.push("-faults", sh(cfg.faults.join(",")));
    const spec = (state.bundle && state.bundle.spec) || cfg.spec;
    if (spec && spec.entry) parts.push("-entry", sh(spec.entry));
    else if (spec && spec.endpoint) parts.push("-endpoint", sh(spec.endpoint));
    return parts.join(" ");
  }

  function sh(s) {
    return /^[A-Za-z0-9_,.:\/=+@-]+$/.test(s) ? s : "'" + String(s).replaceAll("'", "'\\''") + "'";
  }

  // drawGraph builds nodes through the DOM. Node names come from a pasted spec,
  // so interpolating them into markup let a spec ship script into the page.
  function drawGraph(nodes, hit, faulted) {
    const g = $("graph");
    g.innerHTML = "";
    for (const n of nodes) {
      if (n === "end" || n === "abort") continue;
      const li = document.createElement("li");
      if (faulted.has(n)) li.className = "faulted";
      else if (hit.has(n)) li.className = "hit";
      li.textContent = n;
      g.append(li);
    }
  }

  function payload() {
    const extra = selectedExtra();
    const body = {
      seed: seedValue(),
      trials: trialCount(),
      faults: [...state.enabled],
      max_p: 0.3,
      step: 0.01,
      agent: $("agent").value,
      scenario: $("scenario").value,
    };
    if (state.extraScenarios && state.extraScenarios.length) {
      body.extra_scenarios = state.extraScenarios;
    }
    if (state.bundle || extra) {
      body.bundle = {
        spec: (state.bundle && state.bundle.spec) || undefined,
        scenario: extra || (state.bundle && state.bundle.scenario) || {},
      };
    }
    return body;
  }

  // sweep runs one pour at a time. A drop-in sweep can take a minute, and every
  // control calls this, so without the guard the reply from an abandoned
  // configuration could overwrite the one the user is looking at. A request
  // arriving mid-pour is remembered, not dropped: the last configuration asked
  // for is the one that ends up on screen.
  async function sweep() {
    if (state.inflight) {
      state.pending = true;
      return state.inflight;
    }
    $("survival-sub").textContent = "pouring…";
    const run = (async () => {
      try {
        state.sweep = await getJSON("/api/sweep", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload()),
        });
        if (state.selected >= trialCount()) state.selected = 0;
        adoptSweepStep(state.sweep);
        renderSuite();
      } catch (err) {
        state.sweep = null;
        $("survival").textContent = "—";
        $("survival").className = "giant";
        $("survival-sub").textContent = "the pour failed";
        $("headline").textContent = "Could not reach the runner: " + err.message;
      }
    })();
    state.inflight = run;
    await run;
    state.inflight = null;
    if (state.pending) {
      state.pending = false;
      return sweep();
    }
  }

  function onSlider() {
    state.p = Number($("p-slider").value);
    showP();
    renderSuite();
  }

  function showP() {
    $("p-value").textContent = state.p + "%";
    $("p-hint").textContent = heatWord(state.p);
  }

  // adoptSweepStep makes the slider offer only the probabilities that were
  // actually run. A sidecar sweep pays for each suite with a fresh set of trials
  // in another process, so the server coarsens the step; the slider kept its 1%
  // notches regardless, and picking 14% of a sweep taken every 5% showed the
  // nearest suite under a caption reading p=15%.
  function adoptSweepStep(sweep) {
    const pct = Math.round((sweep.step || 0.01) * 100) || 1;
    const el = $("p-slider");
    if (Number(el.step) === pct) return;
    el.step = String(pct);
    state.p = Math.round(state.p / pct) * pct;
    el.value = String(state.p);
    showP();
  }

  // esc escapes quotes as well as angle brackets: some of these strings land in
  // attribute position, where a bare quote is enough to break out.
  function esc(s) {
    return String(s ?? "")
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;")
      .replaceAll("'", "&#39;");
  }

  $("p-slider").addEventListener("input", onSlider);
  $("resweep").addEventListener("click", sweep);
  $("seed").addEventListener("change", sweep);
  $("trials").addEventListener("change", sweep);
  $("agent").addEventListener("change", () => {
    const id = $("agent").value;
    if (applyPreset(id, false)) {
      sweep();
      return;
    }
    const info = (state.meta.agents || []).find((a) => a.id === id);
    if (info) {
      $("agent-name").textContent = info.name;
      $("agent-fw").textContent = info.framework;
    }
    const keep = new Set([
      "pasted",
      "aether-closer-langgraph",
      "aether-closer-adk",
      "ticket-langgraph",
      "ticket-adk",
      "native-langgraph",
      "native-adk",
      "native-openai",
      "native-js",
      "native-react",
      "http-closure",
      "foreign-http",
    ]);
    if (!keep.has(id)) state.bundle = null;
    sweep();
  });
  $("load-ticket-lg").addEventListener("click", () => applyPreset("ticket-langgraph", true));
  $("load-ticket-adk").addEventListener("click", () => applyPreset("ticket-adk", true));
  $("load-native-openai").addEventListener("click", () => applyPreset("native-openai", true));
  $("load-native-js").addEventListener("click", () => applyPreset("native-js", true));
  $("load-native-react").addEventListener("click", () => applyPreset("native-react", true));
  $("load-http-closure").addEventListener("click", () => applyPreset("http-closure", true));
  $("scenario").addEventListener("change", () => {
    const label = scenarioLabel($("scenario").value);
    if (label) $("agent-task").textContent = label;
    sweep();
  });
  $("paste-toggle").addEventListener("click", () => {
    $("paste-panel").hidden = !$("paste-panel").hidden;
  });
  $("paste-apply").addEventListener("click", () => {
    try {
      const raw = JSON.parse($("paste-spec").value);
      state.bundle = raw.spec ? raw : { spec: raw, scenario: raw.scenario || {} };
      if ($("agent").value !== "aether-closer-langgraph" && $("agent").value !== "aether-closer-adk") {
        $("agent").value = "pasted";
      }
      $("agent-name").textContent = (state.bundle.spec && state.bundle.spec.name) || "pasted";
      $("agent-fw").textContent =
        $("agent").value === "aether-closer-langgraph"
          ? "langgraph"
          : $("agent").value === "aether-closer-adk"
            ? "adk"
            : (state.bundle.spec && state.bundle.spec.framework) || "generic";
      fillScenarios();
      $("paste-hint").textContent = "Loaded. Recasting…";
      sweep();
    } catch (err) {
      $("paste-hint").textContent = "Invalid JSON: " + err.message;
    }
  });
  $("gen-scenarios").addEventListener("click", async () => {
    $("gen-scenarios").disabled = true;
    try {
      const drafts = await getJSON("/api/generate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          seed: seedValue(),
          n: 5,
          tools: (state.bundle && state.bundle.spec && state.bundle.spec.tools) || [],
        }),
      });
      state.extraScenarios = drafts || [];
      fillScenarios();
      if (state.extraScenarios.length) {
        $("scenario").value = state.extraScenarios[0].id;
        const sc = state.extraScenarios[0];
        if (sc && sc.name) $("agent-task").textContent = sc.name;
      }
      await sweep();
    } catch (err) {
      $("paste-hint").textContent = "Could not generate scenarios: " + err.message;
    } finally {
      $("gen-scenarios").disabled = false;
    }
  });

  loadMeta().then(sweep).catch((err) => {
    $("headline").textContent = "Could not reach the runner: " + err.message;
    $("survival-sub").textContent = "no runner";
  });
})();
