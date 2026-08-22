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
  };

  const heatWord = (p) => {
    if (p <= 0) return "cold";
    if (p < 8) return "warm";
    if (p < 18) return "forging";
    if (p < 26) return "white-hot";
    return "melt";
  };

  async function loadMeta() {
    const res = await fetch("/api/meta");
    state.meta = await res.json();
    $("agent-name").textContent = state.meta.agent.name;
    $("agent-fw").textContent = state.meta.agent.framework || "langgraph-go";
    $("agent-task").textContent = "Close Acme · email the AE";
    const defaults = state.meta.defaults.faults || [];
    state.enabled = new Set(defaults);
    fillSelect($("agent"), state.meta.agents || [], "id", "id", state.meta.defaults.agent);
    fillScenarios();
    const rt = state.meta.runtime || {};
    $("runtime-pill").textContent = rt.ready ? "runtime: up · langgraph" : "runtime: go only";
    $("ai-pill").textContent = "ai: " + ((state.meta.ai && state.meta.ai.provider) || "local");
    renderFaults();
    drawGraph(graphNodes(), [], []);
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
    if (!state.sweep) return null;
    const want = Math.round(state.p);
    let best = state.sweep.suites[0];
    let dist = 99;
    for (const s of state.sweep.suites) {
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
    const pct = Math.round(suite.survival * 100);
    const el = $("survival");
    el.textContent = pct + "%";
    el.className = "giant " + (pct >= 70 ? "" : pct >= 40 ? "hot" : "dead");
    $("survival-sub").textContent =
      `${suite.trials.length} trials · seed ${suite.config.seed} · p=${Math.round(suite.config.p * 100)}%`;
    const order = ["completed", "recovered", "aborted", "failed"];
    $("counts").innerHTML = order
      .map((k) => {
        const n = suite.counts[k] || 0;
        return `<li><span><i class="dot ${k}"></i> ${k}</span><span>${n}</span></li>`;
      })
      .join("");

    const tiles = $("tiles");
    tiles.innerHTML = "";
    suite.trials.forEach((t) => {
      const b = document.createElement("button");
      b.className = "tile " + t.outcome + (t.n === state.selected ? " sel" : "");
      b.title = `trial ${t.n}: ${t.outcome}${t.faults?.length ? " · " + t.faults.join("+") : ""}`;
      b.addEventListener("click", () => {
        state.selected = t.n;
        renderSuite();
      });
      tiles.append(b);
    });

    const curve = $("curve");
    if (curve && state.sweep) {
      const want = Math.round(state.p);
      curve.innerHTML = state.sweep.suites
        .map((s) => {
          const sp = Math.round((s.config.p || 0) * 100);
          const h = Math.max(2, Math.round((s.survival || 0) * 48));
          const on = sp === want ? "on" : "";
          return `<i class="${on}" style="height:${h}px" title="${sp}% → ${Math.round(s.survival * 100)}%"></i>`;
        })
        .join("");
    }

    $("clusters").innerHTML = suite.clusters
      .slice(0, 8)
      .map((c) => {
        return `<li data-sample="${c.sample_trial}"><span>${esc(c.id)}</span><span>${c.n} · ${Math.round(c.rate * 100)}%</span></li>`;
      })
      .join("");
    $("clusters").querySelectorAll("li").forEach((li) => {
      li.addEventListener("click", () => {
        state.selected = Number(li.dataset.sample);
        renderSuite();
      });
    });

    const c = suite.critique;
    $("headline").textContent = c.headline;
    $("paragraphs").innerHTML = (c.paragraphs || []).map((p) => `<p>${esc(p)}</p>`).join("");
    $("fixes").innerHTML = (c.fixes || [])
      .map((f) => `<li><code>${esc(f.node)}</code> — ${esc(f.advice)}</li>`)
      .join("");

    const trial = suite.trials.find((t) => t.n === state.selected) || suite.trials[0];
    if (trial) renderTrial(suite, trial);

    const heat = Math.min(1, state.p / 30);
    document.documentElement.style.setProperty("--heat", String(0.08 + heat * 0.55));
  }

  function renderTrial(suite, trial) {
    $("tl-title").textContent = `Trial ${trial.n} · ${trial.outcome}`;
    $("tl-reason").textContent = trial.reason;
    $("replay-cmd").textContent =
      `crucible replay -seed ${suite.config.seed} -trial ${trial.n} -p ${suite.config.p}`;

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
    tl.innerHTML = (trial.events || [])
      .map((ev) => {
        const cls = ev.kind === "fault" ? "fault" : ev.kind;
        return `<li class="${cls}"><span>${ev.tick}</span><span class="kind">${esc(ev.kind)}</span><span>${esc(ev.message)}</span></li>`;
      })
      .join("");
  }

  function drawGraph(nodes, hit, faulted) {
    $("graph").innerHTML = nodes
      .filter((n) => n !== "end" && n !== "abort")
      .map((n) => {
        const cls = faulted.has(n) ? "faulted" : hit.has(n) ? "hit" : "";
        return `<li class="${cls}">${n}</li>`;
      })
      .join("");
  }

  function payload() {
    const extra = selectedExtra();
    const body = {
      seed: Number($("seed").value) || 42,
      trials: Number($("trials").value) || 40,
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

  async function sweep() {
    $("survival-sub").textContent = "pouring…";
    const res = await fetch("/api/sweep", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload()),
    });
    state.sweep = await res.json();
    if (state.selected >= (Number($("trials").value) || 40)) state.selected = 0;
    renderSuite();
  }

  function onSlider() {
    state.p = Number($("p-slider").value);
    $("p-value").textContent = state.p + "%";
    $("p-hint").textContent = heatWord(state.p);
    renderSuite();
  }

  function esc(s) {
    return String(s ?? "")
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;");
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
    ]);
    if (!keep.has(id)) state.bundle = null;
    sweep();
  });
  $("load-ticket-lg").addEventListener("click", () => applyPreset("ticket-langgraph", true));
  $("load-ticket-adk").addEventListener("click", () => applyPreset("ticket-adk", true));
  $("load-native-openai").addEventListener("click", () => applyPreset("native-openai", true));
  $("load-native-js").addEventListener("click", () => applyPreset("native-js", true));
  $("scenario").addEventListener("change", () => {
    const extra = selectedExtra();
    if (extra) {
      $("agent-task").textContent = extra.name;
      sweep();
      return;
    }
    const items = [...(state.meta.scenarios || []), ...extraItems()];
    const sc = items.find((s) => s.id === $("scenario").value);
    if (sc) $("agent-task").textContent = sc.name;
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
      const res = await fetch("/api/generate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          seed: Number($("seed").value) || 42,
          n: 5,
          tools: (state.bundle && state.bundle.spec && state.bundle.spec.tools) || [],
        }),
      });
      const drafts = await res.json();
      state.extraScenarios = drafts || [];
      fillScenarios();
      if (state.extraScenarios.length) {
        $("scenario").value = state.extraScenarios[0].id;
        const sc = state.extraScenarios[0];
        if (sc && sc.name) $("agent-task").textContent = sc.name;
      }
      sweep();
    } finally {
      $("gen-scenarios").disabled = false;
    }
  });

  loadMeta().then(sweep).catch((err) => {
    $("headline").textContent = "Could not reach the runner: " + err.message;
  });
})();
